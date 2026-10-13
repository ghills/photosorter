package takeout

import (
	"strings"
	"testing"
	"time"
)

func TestReadMetadata(t *testing.T) {
	m, err := ReadMetadata(strings.NewReader(`{
		"title": "test.mp4",
		"photoTakenTime": {"timestamp": "1672531200", "formatted": "Jan 1, 2023, 12:00:00 AM UTC"},
		"creationTime": {"timestamp": "0"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "test.mp4" {
		t.Errorf("Title = %q", m.Title)
	}
	if got, ok := m.PhotoTakenTime.Time(); !ok || !got.Equal(time.Unix(1672531200, 0)) {
		t.Errorf("PhotoTakenTime = %v, %v", got, ok)
	}
	if _, ok := m.CreationTime.Time(); ok {
		t.Error("a zero timestamp means unknown")
	}
}

func TestTimestamp_Invalid(t *testing.T) {
	for _, v := range []string{"", "abc", "-5", "0"} {
		if _, ok := (Timestamp{Timestamp: v}).Time(); ok {
			t.Errorf("Timestamp %q should be rejected", v)
		}
	}
}

func TestReadMetadata_BadJSON(t *testing.T) {
	if _, err := ReadMetadata(strings.NewReader("{not json")); err == nil {
		t.Error("expected a decode error")
	}
}
