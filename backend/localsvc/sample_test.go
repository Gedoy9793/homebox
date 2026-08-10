package localsvc

import (
	"os"
	"testing"
)

func TestGenerateSampleLabels(t *testing.T) {
	if os.Getenv("SAMPLE_OUT") == "" {
		t.Skip("set SAMPLE_OUT to write sample labels")
	}

	cases := map[string]struct {
		request labelRequest
		prof    profile
	}{
		"standard": {
			request: labelRequest{
				title:       "000-042",
				description: "Netgear GS308 switch\nLocation: Rack 3 / Shelf 2",
				url:         "https://homebox.example.com/a/000-042",
			},
			prof: profiles[profileStandard],
		},
		"cable": {
			request: labelRequest{
				title:       testCableID,
				description: "Office AP uplink from the patch panel in rack 3",
				assetID:     "000-042",
				url:         "https://homebox.example.com/item/abc",
			},
			prof: profiles[profileCable],
		},
		"chinese": {
			request: labelRequest{
				title:       "000-108",
				description: "三养辣鸡肉芝士味拌面（油炸方便面）\n位置：储物间 / 第二层",
				url:         "https://homebox.example.com/a/000-108",
			},
			prof: profiles[profileStandard],
		},
	}

	for name, testCase := range cases {
		raw, err := renderLabel(testCase.request, testCase.prof)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := os.WriteFile(os.Getenv("SAMPLE_OUT")+"/label-"+name+".png", raw, 0o644); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s: %d bytes", name, len(raw))
	}
}
