package gateway

import (
	"testing"
	"testing/fstest"
)

func ptr(s string) *string { return &s }

func TestNewVehicleImageResolver(t *testing.T) {
	// Fake embedded FS with two known images + the default.
	fsys := fstest.MapFS{
		"static/img/modelyPearlWhite.png": {},
		"static/img/model3SolidBlack.png": {},
		"static/img/defaultCar.png":       {},
		"static/img/README.txt":           {}, // non-png, must be ignored
	}
	resolver := newVehicleImageResolver(fsys)

	cases := []struct {
		name    string
		carType *string
		color   *string
		want    string
	}{
		{"known pair", ptr("modely"), ptr("PearlWhite"), "/static/img/modelyPearlWhite.png"},
		{"known pair model3", ptr("model3"), ptr("SolidBlack"), "/static/img/model3SolidBlack.png"},
		{"unknown color", ptr("modely"), ptr("DeepBlue"), "/static/img/defaultCar.png"},
		{"unknown carType", ptr("models"), ptr("PearlWhite"), "/static/img/defaultCar.png"},
		{"nil carType", nil, ptr("PearlWhite"), "/static/img/defaultCar.png"},
		{"nil color", ptr("modely"), nil, "/static/img/defaultCar.png"},
		{"empty carType", ptr(""), ptr("PearlWhite"), "/static/img/defaultCar.png"},
		{"empty color", ptr("modely"), ptr(""), "/static/img/defaultCar.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolver(tc.carType, tc.color); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewVehicleImageResolver_MissingImgDir(t *testing.T) {
	// No static/img subtree → empty known set → always default.
	resolver := newVehicleImageResolver(fstest.MapFS{})
	if got := resolver(ptr("modely"), ptr("PearlWhite")); got != vehicleImageDefault {
		t.Fatalf("got %q, want %q", got, vehicleImageDefault)
	}
}
