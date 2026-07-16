package processors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFxOutValidateCommand_MissingQuoteID(t *testing.T) {
	p := NewFxOut(nil)
	err := p.ValidateCommand(context.Background(), Command{
		Metadata: map[string]any{"rate": "15800", "pair": "IDRUSD"},
	})
	assert.Error(t, err)
}

func TestFxOutValidateCommand_MissingRate(t *testing.T) {
	p := NewFxOut(nil)
	err := p.ValidateCommand(context.Background(), Command{
		Metadata: map[string]any{"quote_id": "q1", "pair": "IDRUSD"},
	})
	assert.Error(t, err)
}

func TestFxOutValidateCommand_MissingPair(t *testing.T) {
	p := NewFxOut(nil)
	err := p.ValidateCommand(context.Background(), Command{
		Metadata: map[string]any{"quote_id": "q1", "rate": "15800"},
	})
	assert.Error(t, err)
}

func TestFxOutValidateCommand_AllPresent(t *testing.T) {
	p := NewFxOut(nil)
	err := p.ValidateCommand(context.Background(), Command{
		Metadata: map[string]any{"quote_id": "q1", "rate": "15800", "pair": "IDRUSD"},
	})
	assert.NoError(t, err)
}

func TestFxInValidateCommand_MissingQuoteID(t *testing.T) {
	p := NewFxIn(nil)
	err := p.ValidateCommand(context.Background(), Command{
		Metadata: map[string]any{"rate": "15800", "pair": "IDRUSD"},
	})
	assert.Error(t, err)
}

func TestFxInValidateCommand_MissingRate(t *testing.T) {
	p := NewFxIn(nil)
	err := p.ValidateCommand(context.Background(), Command{
		Metadata: map[string]any{"quote_id": "q1", "pair": "IDRUSD"},
	})
	assert.Error(t, err)
}

func TestFxInValidateCommand_MissingPair(t *testing.T) {
	p := NewFxIn(nil)
	err := p.ValidateCommand(context.Background(), Command{
		Metadata: map[string]any{"quote_id": "q1", "rate": "15800"},
	})
	assert.Error(t, err)
}

func TestFxInValidateCommand_AllPresent(t *testing.T) {
	p := NewFxIn(nil)
	err := p.ValidateCommand(context.Background(), Command{
		Metadata: map[string]any{"quote_id": "q1", "rate": "15800", "pair": "IDRUSD"},
	})
	assert.NoError(t, err)
}

func TestFxOutType(t *testing.T) {
	assert.Equal(t, "fx_out", NewFxOut(nil).Type())
}

func TestFxInType(t *testing.T) {
	assert.Equal(t, "fx_in", NewFxIn(nil).Type())
}
