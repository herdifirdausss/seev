package privacyexport

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAndNext(t *testing.T) {
	offset, size, err := Parse(url.Values{"cursor": {"100"}, "page_size": {"50"}})
	require.NoError(t, err)
	require.Equal(t, 100, offset)
	require.Equal(t, 50, size)
	require.Equal(t, "150", Next(offset, size, true))
	require.Empty(t, Next(offset, size, false))
}

func TestParseRejectsUnboundedOrMalformedPages(t *testing.T) {
	for _, values := range []url.Values{
		{"page_size": {"0"}},
		{"page_size": {"501"}},
		{"cursor": {"-1"}},
		{"cursor": {"not-a-number"}},
	} {
		_, _, err := Parse(values)
		require.Error(t, err)
	}
}
