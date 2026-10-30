package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The generated config.json should mention every option, so users can find
// them without reading the source.
func TestDefaultTemplateListsEveryOption(t *testing.T) {
	var got map[string]json.RawMessage
	if err := json.Unmarshal(stripComments([]byte(defaultTemplate)), &got); err != nil {
		t.Fatalf("template is not valid JSON: %v", err)
	}
	var walk func(rt reflect.Type)
	walk = func(rt reflect.Type) {
		for f := range rt.Fields() {
			if f.Anonymous {
				walk(f.Type)
				continue
			}
			key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if _, ok := got[key]; !ok {
				t.Errorf("option %q is missing from the default config template", key)
			}
		}
	}
	walk(reflect.TypeFor[Config]())

	// and the template must load back to the defaults
	cfg := defaults()
	if err := json.Unmarshal(stripComments([]byte(defaultTemplate)), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AudioFormatOrDefault() != "mp3" || cfg.Player() != "mpv" || cfg.PreviewWait() != 5*time.Second+time.Duration(cfg.PreviewWaitSeconds)*time.Second {
		t.Errorf("template changes the defaults: %+v", cfg.DownloadOptions)
	}
}
