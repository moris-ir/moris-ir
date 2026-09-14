package bot

import (
	"moris/lib"
	"testing"
)

func TestSafeFilenameIntegration(t *testing.T) {
	if got := lib.SafeFilename("../../etc/passwd"); got != "passwd" {
		t.Fatalf("got %q", got)
	}
}
