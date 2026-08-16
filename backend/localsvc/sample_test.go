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
				title:   "Netgear GS308 switch",
				footer:  "Rack 3 / Shelf 2",
				assetID: testAssetID,
				tags:    []string{"network", "core"},
				url:     testAssetURL,
			},
			prof: profiles[profileStandard],
		},
		"cable": {
			request: labelRequest{
				title:   testCableID,
				detail:  "Office AP uplink from the patch panel in rack 3",
				footer:  "Rack 3",
				assetID: testAssetID,
				tags:    []string{"uplink", "office", "rack-a", "poe", "trunk"},
				url:     "https://homebox.example.com/item/abc",
			},
			prof: profiles[profileCable],
		},
		"location": {
			request: labelRequest{
				title:   "第二层",
				detail:  "备件与耗材",
				footer:  "一楼 / 车库 / 货架 A / 左侧 / 第二层",
				assetID: "000-007",
				url:     "https://homebox.example.com/location/abc",
			},
			prof: profiles[profileLocation],
		},
		"chinese": {
			request: labelRequest{
				title:   "三养辣鸡肉芝士味拌面（油炸方便面）",
				footer:  "储物间 / 第二层",
				assetID: "000-108",
				url:     "https://homebox.example.com/a/000-108",
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
