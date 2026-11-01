package daemon

import (
	"testing"
	"time"
)

// FastEmbedWorker shortens the embedding worker's waits for the rest of the test.
func FastEmbedWorker(t *testing.T) {
	idle, lo, hi := idleTick, minBackoff, maxBackoff
	idleTick, minBackoff, maxBackoff = 20*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { idleTick, minBackoff, maxBackoff = idle, lo, hi })
}
