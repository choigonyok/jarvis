package push

import (
	"encoding/base64"
	"testing"
)

// Apple refuses a topic that is not URL-safe base64 of at most 32 characters
// (BadWebPushTopic) - which "suggestionnew" was not.
func TestTopicIsValidBase64(t *testing.T) {
	for _, tag := range []string{"suggestion.new", "group", "test", "approval.pending", "status.fail"} {
		got := topic(tag)
		if len(got) == 0 || len(got) > 32 {
			t.Fatalf("%s: %q", tag, got)
		}
		if _, err := base64.RawURLEncoding.DecodeString(got); err != nil {
			t.Fatalf("%s: %q is not base64: %v", tag, got, err)
		}
		if topic(tag) != got {
			t.Fatalf("%s: not stable", tag)
		}
	}
	if topic("suggestion.new") == topic("group") {
		t.Fatal("different tags share a topic")
	}
}
