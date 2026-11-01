package app

import (
	"testing"
	"time"
)

// SetQueryEmbedTimeout shortens the search query embedding timeout for the rest of the test.
func SetQueryEmbedTimeout(t *testing.T, d time.Duration) {
	old := queryEmbedTimeout
	queryEmbedTimeout = d
	t.Cleanup(func() { queryEmbedTimeout = old })
}
