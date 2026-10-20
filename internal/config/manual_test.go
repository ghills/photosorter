package config

import (
	"strings"
	"testing"
)

func TestValidate_ManualImport(t *testing.T) {
	withInput := func(m Manual) Config {
		c := valid()
		c.Sync = false
		c.Folders = []string{"/scans"}
		c.Manual = m
		return c
	}
	accept := map[string]Config{
		"forced date and event": withInput(Manual{Date: "2019-07-04", Event: "Fireworks"}),
		"year-only fallback":    withInput(Manual{FallbackDate: "1998"}),
		"event alone":           withInput(Manual{Event: "Hawaii trip"}),
		"album with albums=ignore": func() Config {
			c := withInput(Manual{Album: "Grandma's box"})
			c.Layout.Albums = AlbumsIgnore
			return c
		}(),
	}
	for name, c := range accept {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}

	noInput := valid() // -sync only
	noInput.Manual = Manual{Date: "2019"}
	reject := map[string]struct {
		cfg  Config
		want string
	}{
		"bad date":         {withInput(Manual{Date: "2019-02-30"}), "-date"},
		"bad fallback":     {withInput(Manual{FallbackDate: "07/2019"}), "-fallback-date"},
		"both dates":       {withInput(Manual{Date: "2019", FallbackDate: "2018"}), "not both"},
		"album and event":  {withInput(Manual{Album: "A", Event: "B"}), "not both"},
		"event with slash": {withInput(Manual{Event: "a/b"}), "single folder name"},
		"album dot-dot":    {withInput(Manual{Album: ".."}), "single folder name"},
		"event colon":      {withInput(Manual{Event: "Trip: day 1"}), "single folder name"},
		"trailing space":   {withInput(Manual{Album: "Trip "}), "single folder name"},
		"no input":         {noInput, "need something to import"},
	}
	for name, tc := range reject {
		err := tc.cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want one mentioning %q", name, err, tc.want)
		}
	}
}

func TestLoadFile_ManualSettingsAreCommandLineOnly(t *testing.T) {
	for _, key := range []string{"date", "fallback_date", "event", "album"} {
		path := writeConfig(t, key+` = "2019"`)
		c := Default()
		err := LoadFile(path, &c)
		if err == nil || !strings.Contains(err.Error(), "only be given on the command line") {
			t.Errorf("%s: error %v, want a command-line-only error", key, err)
		}
	}
}
