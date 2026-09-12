package types

import (
	"encoding/json"
	"testing"
)

// TestWikiConfigLearningFeaturesBackwardCompat proves the migration story
// the design doc promises for the KB-level learning switch: rows written
// before the field existed (no learning_features key) must scan as "off",
// not error, not default on.
func TestWikiConfigLearningFeaturesBackwardCompat(t *testing.T) {
	// A pre-learning row, exactly the shape older deployments wrote.
	legacy := []byte(`{"synthesis_model_id":"m-1","max_pages_per_ingest":0,"extraction_granularity":"standard"}`)
	var cfg WikiConfig
	if err := json.Unmarshal(legacy, &cfg); err != nil {
		t.Fatalf("legacy json must scan: %v", err)
	}
	if cfg.LearningFeatures {
		t.Fatal("legacy row must read as learning off")
	}

	// Scan path (the one the database actually uses).
	var scanned WikiConfig
	if err := scanned.Scan(legacy); err != nil {
		t.Fatalf("legacy scan must succeed: %v", err)
	}
	if scanned.LearningFeatures {
		t.Fatal("legacy scan must read as learning off")
	}
}

// TestWikiConfigLearningFeaturesRoundTrip proves a switched-on config
// survives the Value/Scan pair with the flag intact.
func TestWikiConfigLearningFeaturesRoundTrip(t *testing.T) {
	cfg := WikiConfig{SynthesisModelID: "m-1", LearningFeatures: true}
	raw, err := cfg.Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	var back WikiConfig
	if err := back.Scan(raw); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !back.LearningFeatures {
		t.Fatal("round trip lost the learning flag")
	}
	if back.SynthesisModelID != "m-1" {
		t.Fatal("round trip lost sibling field")
	}

	// And the JSON key name is stable for the settings UI contract.
	keys := map[string]any{}
	if err := json.Unmarshal(raw.([]byte), &keys); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["learning_features"]; !ok {
		t.Fatal("json key must be learning_features")
	}
}
