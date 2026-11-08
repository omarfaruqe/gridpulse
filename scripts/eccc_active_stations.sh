#!/bin/sh
# Lists major-city ECCC hourly stations that are reporting now, by province,
# and checks that the datetime filter works. Read-only; uses curl so it relies
# on the system certificate store.
BASE="https://api.weather.gc.ca/collections/climate-hourly/items"

for prov in QC ON AB; do
  curl -s -G "$BASE" --data-urlencode "f=json" --data-urlencode "PROVINCE_CODE=$prov" \
    --data-urlencode "limit=500" --data-urlencode "sortby=-LOCAL_DATE" |
  python3 -c '
import sys, json, re
d = json.load(sys.stdin)
seen = {}
for f in d["features"]:
    p = f["properties"]
    seen.setdefault(p["CLIMATE_IDENTIFIER"], p)
pat = re.compile(r"MONTREAL|TORONTO|OTTAWA|QU.BEC|CALGARY|EDMONTON|PEARSON|TRUDEAU|HAMILTON|LONDON|WINDSOR|SHERBROOKE|GATINEAU|RED DEER|LETHBRIDGE|MCMURRAY")
print(sys.argv[1], "- rows:", len(d["features"]), "distinct stations:", len(seen))
for c, p in sorted(seen.items(), key=lambda kv: kv[1]["STATION_NAME"]):
    if pat.search(p["STATION_NAME"]):
        print("  %s | %s | %.2f,%.2f | latest UTC %s" % (c, p["STATION_NAME"], p["LATITUDE_DECIMAL_DEGREES"], p["LONGITUDE_DECIMAL_DEGREES"], p["UTC_DATE"]))
' "$prov"
done

echo "--- datetime filter test (QC, a 3-hour window two days ago):"
DAY=$(python3 -c 'import datetime; print((datetime.datetime.now(datetime.timezone.utc)-datetime.timedelta(days=2)).strftime("%Y-%m-%d"))')
curl -s -G "$BASE" --data-urlencode "f=json" --data-urlencode "PROVINCE_CODE=QC" \
  --data-urlencode "datetime=${DAY}T00:00:00Z/${DAY}T03:00:00Z" --data-urlencode "limit=500" |
python3 -c '
import sys, json
d = json.load(sys.stdin)
u = sorted(x["properties"]["UTC_DATE"] for x in d["features"])
print("window start:", sys.argv[1], "matched:", d.get("numberMatched"), "returned:", d.get("numberReturned"), "min:", u[0] if u else None, "max:", u[-1] if u else None)
' "$DAY"
