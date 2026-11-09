// Draws the dashboard charts from the JSON API. No inline script: the page's
// content-security policy only allows scripts served from this origin.
(() => {
  'use strict';
  const lang = document.documentElement.lang === 'fr' ? 'fr' : 'en';
  const css = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  const colour = (i) => css('--c' + ((i % 8) + 1));
  const fmt = new Intl.NumberFormat(lang === 'fr' ? 'fr-CA' : 'en-CA', { maximumFractionDigits: 1 });

  Chart.defaults.color = css('--muted');
  Chart.defaults.borderColor = css('--line');
  Chart.defaults.font.family = getComputedStyle(document.body).fontFamily;
  Chart.defaults.animation = false;

  // "2026-11-07T05:00:00Z" -> "11-07 05:00"
  const hourLabel = (iso) => iso.slice(5, 10) + ' ' + iso.slice(11, 16);

  async function getJSON(url) {
    const res = await fetch(url, { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new Error(url + ' answered ' + res.status);
    return (await res.json()).data;
  }

  function axes(ylabel, xlabel) {
    return {
      x: { title: { display: !!xlabel, text: xlabel }, ticks: { maxTicksLimit: 8, maxRotation: 0, autoSkipPadding: 28 }, grid: { display: false } },
      y: { title: { display: !!ylabel, text: ylabel }, grid: { color: css('--line') } },
    };
  }

  function base(extra) {
    return Object.assign({
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: false },
      plugins: { legend: { display: false }, tooltip: { callbacks: { label: (c) => `${c.dataset.label}: ${fmt.format(c.parsed.y)}` } } },
    }, extra);
  }

  const builders = {
    load(rows, el) {
      return {
        type: 'line',
        data: { labels: rows.map((r) => hourLabel(r.hour_utc)), datasets: [{
          label: el.dataset.ylabel, data: rows.map((r) => r.load_mw), // null stays a gap, never 0
          borderColor: colour(0), borderWidth: 2, pointRadius: 0, spanGaps: false,
        }] },
        options: base({ scales: axes(el.dataset.ylabel) }),
      };
    },

    price(rows, el) {
      return {
        type: 'line',
        data: { labels: rows.map((r) => hourLabel(r.hour_utc)), datasets: [{
          label: el.dataset.ylabel, data: rows.map((r) => r.price_cad_mwh),
          borderColor: colour(1), borderWidth: 2, pointRadius: 0, spanGaps: false, stepped: true,
        }] },
        options: base({ scales: axes(el.dataset.ylabel) }),
      };
    },

    scatter(rows, el) {
      return {
        type: 'scatter',
        data: { datasets: [{
          label: el.dataset.ylabel, data: rows.map((r) => ({ x: r.temp_c, y: r.load_mw })),
          backgroundColor: colour(2), pointRadius: 3,
        }] },
        options: base({
          interaction: { mode: 'nearest', intersect: true },
          scales: axes(el.dataset.ylabel, el.dataset.xlabel),
          plugins: { legend: { display: false }, tooltip: { callbacks: { label: (c) => `${fmt.format(c.parsed.x)} °C → ${fmt.format(c.parsed.y)} MW` } } },
        }),
      };
    },

    async generation(rows, el) {
      const fuels = await getJSON('/api/v1/fuels');
      const names = Object.fromEntries(fuels.map((f) => [f.fuel_type, lang === 'fr' ? f.name_fr : f.name_en]));
      const hours = [...new Set(rows.map((r) => r.hour_utc))];
      const byFuel = new Map();
      for (const r of rows) {
        if (!byFuel.has(r.fuel_type)) byFuel.set(r.fuel_type, new Map());
        byFuel.get(r.fuel_type).set(r.hour_utc, r.generation_mw);
      }
      const mean = (m) => { const v = [...m.values()].filter((x) => x != null); return v.reduce((a, b) => a + b, 0) / (v.length || 1); };
      const order = [...byFuel.keys()].sort((a, b) => mean(byFuel.get(b)) - mean(byFuel.get(a)));
      return {
        type: 'line',
        data: { labels: hours.map(hourLabel), datasets: order.map((fuel, i) => ({
          label: names[fuel] || fuel, data: hours.map((h) => byFuel.get(fuel).get(h) ?? null),
          backgroundColor: colour(i), borderColor: colour(i), borderWidth: 0.5, pointRadius: 0, fill: true, stack: 'gen',
        })) },
        options: base({
          scales: Object.assign(axes(el.dataset.ylabel), { y: { stacked: true, beginAtZero: true, title: { display: true, text: el.dataset.ylabel }, grid: { color: css('--line') } } }),
          plugins: { legend: { display: true, position: 'bottom' }, tooltip: { callbacks: { label: (c) => `${c.dataset.label}: ${fmt.format(c.parsed.y)}` } } },
        }),
      };
    },

    recon(rows, el) {
      const usable = rows.filter((r) => r.ratio != null);
      const months = [...new Set(usable.map((r) => r.month))].sort();
      const groups = [...new Set(usable.map((r) => r.group))].sort();
      return {
        type: 'line',
        data: { labels: months, datasets: groups.map((g, i) => {
          const byMonth = new Map(usable.filter((r) => r.group === g).map((r) => [r.month, r]));
          const at = (m) => byMonth.get(m);
          return {
            label: g, data: months.map((m) => at(m)?.ratio ?? null),
            borderColor: colour(i), backgroundColor: colour(i), borderWidth: 2, spanGaps: false,
            // a triangle marks a month outside its tolerance band, so colour is not the only cue
            pointStyle: months.map((m) => (at(m)?.status === 'out_of_band' ? 'triangle' : 'circle')),
            pointRadius: months.map((m) => (at(m)?.status === 'out_of_band' ? 7 : 3)),
          };
        }) },
        options: base({
          scales: Object.assign(axes(el.dataset.ylabel), { y: { min: 0, title: { display: true, text: el.dataset.ylabel }, grid: { color: css('--line') } } }),
          plugins: { legend: { display: true, position: 'bottom' }, tooltip: { callbacks: { label: (c) => `${c.dataset.label}: ${c.parsed.y.toFixed(3)}` } } },
        }),
      };
    },
  };

  async function draw(canvas) {
    const card = canvas.closest('.card');
    const empty = card.querySelector('.empty');
    try {
      const rows = await getJSON(canvas.dataset.url);
      if (rows.length === 0) throw new Error('empty');
      const config = await builders[canvas.dataset.chart](rows, canvas);
      new Chart(canvas, config);
    } catch (err) {
      canvas.parentElement.hidden = true;
      if (empty) empty.hidden = false;
      if (err.message !== 'empty') console.error(err);
    }
  }

  document.querySelectorAll('canvas[data-chart]').forEach(draw);
})();
