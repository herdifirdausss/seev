package cryptox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskEmail_Deterministic(t *testing.T) {
	a := MaskEmail("mia@example.test")
	b := MaskEmail("mia@example.test")
	assert.Equal(t, a, b)
}

func TestMaskEmail_NeverContainsOriginalLocalPart(t *testing.T) {
	masked := MaskEmail("noah.wallace@example.test")
	assert.NotContains(t, masked, "noah.wallace")
	assert.Contains(t, masked, "n***@")
	assert.Contains(t, masked, ".test")
}

func TestMaskEmail_NoAtSign(t *testing.T) {
	assert.Equal(t, "***", MaskEmail("not-an-email"))
	assert.Equal(t, "***", MaskEmail(""))
}

func TestMaskEmail_DomainWithoutDot(t *testing.T) {
	assert.Equal(t, "m***@l***", MaskEmail("mia@localhost"))
}
