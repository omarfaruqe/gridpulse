package statcan

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/generation.csv")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseGeneration(t *testing.T) {
	rows, err := ParseGeneration(fixture(t)) // the fixture starts with a UTF-8 BOM
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(rows))
	}
	hydro := rows[1]
	if hydro.Geo != "Ontario" || hydro.GenerationType != "Hydraulic turbine" ||
		hydro.MWh == nil || *hydro.MWh != 2400000 || !hydro.Month.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Ontario hydro = %+v", hydro)
	}
	// A blank VALUE stays nil (not 0) and keeps its status symbol.
	if gap := rows[2]; gap.MWh != nil || gap.Status != ".." {
		t.Errorf("unavailable row = %+v, want nil value with status ..", gap)
	}
	if est := rows[4]; est.MWh == nil || est.Status != "E" {
		t.Errorf("estimated row = %+v, want a value with status E", est)
	}
}

func TestParseGenerationRejectsDrift(t *testing.T) {
	good := string(fixture(t))
	cases := map[string]string{
		"renamed column": strings.Replace(good, `"Type of electricity generation"`, `"Generation type"`, 1),
		"unit changed":   strings.Replace(good, "Megawatt hours", "Gigawatt hours", 1),
		"scale changed":  strings.Replace(good, `"units"`, `"thousands"`, 1),
		"bad month":      strings.Replace(good, `"2026-08","Canada"`, `"Aug 2026","Canada"`, 1),
		"bad value":      strings.Replace(good, `"2400000"`, `"lots"`, 1),
		"duplicate row":  good + `"2026-08","Ontario","x","Total all classes of electricity producer","Hydraulic turbine","Megawatt hours","210","units","0","v","7.1.2","1","","","","0"` + "\n",
		"short row":      good + `"2026-08","Ontario"` + "\n",
		"header only":    strings.SplitN(good, "\n", 2)[0] + "\n",
		"empty":          "",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseGeneration([]byte(doc)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestParseGenerationFindsColumnsByName(t *testing.T) {
	// Same data with the value columns moved to the front still parses.
	csv := `"VALUE","STATUS","VECTOR","UOM","SCALAR_FACTOR","REF_DATE","GEO","Class of electricity producer","Type of electricity generation"
"5","","v1","Megawatt hours","units","2026-01","Ontario","Total all classes of electricity producer","Solar"
`
	rows, err := ParseGeneration([]byte(csv))
	if err != nil || len(rows) != 1 || rows[0].MWh == nil || *rows[0].MWh != 5 || rows[0].GenerationType != "Solar" {
		t.Errorf("rows = %+v, err = %v", rows, err)
	}
}

func makeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type put struct{ source, date, filename, contentType string }

type recorder struct{ puts []put }

func (r *recorder) Put(_ context.Context, source string, date time.Time, filename, contentType string, _ []byte) (string, error) {
	r.puts = append(r.puts, put{source, date.Format("2006-01-02"), filename, contentType})
	return source + "/" + filename, nil
}

func serve(t *testing.T, archive []byte) (*Extractor, *recorder, *string) {
	t.Helper()
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	rec := &recorder{}
	ex := New(rec)
	ex.BaseURL = srv.URL
	ex.Attempts = 1
	ex.Now = func() time.Time { return time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC) }
	return ex, rec, &path
}

func TestFetchGenerationStoresContentAddressedCSV(t *testing.T) {
	archive := makeZip(t, map[string][]byte{
		"25100015.csv":          fixture(t),
		"25100015_MetaData.csv": []byte("not the data"),
	})
	ex, rec, path := serve(t, archive)

	res, err := ex.FetchGeneration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if *path != "/25100015-eng.zip" {
		t.Errorf("requested %s", *path)
	}
	if res.Rows != 5 {
		t.Errorf("rows = %d, want 5", res.Rows)
	}
	if len(rec.puts) != 1 || rec.puts[0].source != Source || rec.puts[0].date != "2026-11-08" ||
		rec.puts[0].contentType != "text/csv" || !strings.HasPrefix(rec.puts[0].filename, "25100015_") {
		t.Fatalf("puts = %+v", rec.puts)
	}
	// The same table downloaded again maps to the same key.
	first := rec.puts[0].filename
	if _, err := ex.FetchGeneration(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.puts[1].filename != first {
		t.Errorf("filename changed between identical downloads: %s vs %s", first, rec.puts[1].filename)
	}
}

func TestFetchGenerationRejectsBadDownloads(t *testing.T) {
	cases := map[string][]byte{
		"not a zip":        []byte("<html>maintenance</html>"),
		"csv missing":      makeZip(t, map[string][]byte{"other.csv": []byte("x")}),
		"csv fails checks": makeZip(t, map[string][]byte{"25100015.csv": []byte("a,b\n1,2\n")}),
	}
	for name, archive := range cases {
		t.Run(name, func(t *testing.T) {
			ex, rec, _ := serve(t, archive)
			if _, err := ex.FetchGeneration(context.Background()); err == nil {
				t.Error("expected an error")
			}
			if len(rec.puts) != 0 {
				t.Errorf("stored %d objects, want none for an invalid download", len(rec.puts))
			}
		})
	}
}
