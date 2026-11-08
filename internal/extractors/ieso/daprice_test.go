package ieso

import (
	"os"
	"testing"
	"time"
)

func TestParseDAHourlyOntarioZonalPrice(t *testing.T) {
	data, err := os.ReadFile("testdata/DAHourlyOntarioZonalPrice.xml")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ParseDAHourlyOntarioZonalPrice(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if want := time.Date(2026, 11, 6, 5, 0, 0, 0, time.UTC); !rows[0].IntervalStartUTC.Equal(want) {
		t.Errorf("IntervalStartUTC = %v, want %v", rows[0].IntervalStartUTC, want)
	}
	if rows[0].ZonalPrice == nil || *rows[0].ZonalPrice != 38.71 {
		t.Errorf("ZonalPrice = %v, want 38.71", rows[0].ZonalPrice)
	}
	if rows[0].Flag != "DSO-RD" {
		t.Errorf("Flag = %q", rows[0].Flag)
	}
	// An empty element must stay nil: a blank price is not a $0.00 price.
	if rows[2].ZonalPrice != nil || rows[2].LossPrice != nil || rows[2].CongestionPrice != nil {
		t.Error("empty price elements must be nil, not 0")
	}
}

func TestParseDAPriceRejectsDrift(t *testing.T) {
	for name, in := range map[string]string{
		"not xml": `nope`,
		"no rows": `<Document><DocBody><DeliveryDate>2026-11-06</DeliveryDate></DocBody></Document>`,
		"bad date": `<Document><DocBody><DeliveryDate>x</DeliveryDate><HourlyPriceComponents>` +
			`<PricingHour>1</PricingHour></HourlyPriceComponents></DocBody></Document>`,
		"bad hour": `<Document><DocBody><DeliveryDate>2026-11-06</DeliveryDate><HourlyPriceComponents>` +
			`<PricingHour>30</PricingHour></HourlyPriceComponents></DocBody></Document>`,
		"bad price": `<Document><DocBody><DeliveryDate>2026-11-06</DeliveryDate><HourlyPriceComponents>` +
			`<PricingHour>1</PricingHour><ZonalPrice>abc</ZonalPrice></HourlyPriceComponents></DocBody></Document>`,
	} {
		if _, err := ParseDAHourlyOntarioZonalPrice([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseGenEmptyEnergyStaysNil(t *testing.T) {
	in := `<IMODocument><IMODocBody><Date>2026-11-06</Date><Generators><Generator><GeneratorName>A</GeneratorName>` +
		`<FuelType>GAS</FuelType><Outputs><Output><Hour>1</Hour><EnergyMW/></Output></Outputs></Generator></Generators></IMODocBody></IMODocument>`
	rows, err := ParseGenOutputCapability([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].EnergyMW != nil {
		t.Errorf("empty <EnergyMW/> must be nil, got %+v", rows)
	}
}
