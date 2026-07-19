package fraud

import "errors"

var ErrInvalidScreenInput = errors.New("invalid fraud screen input")
var ErrInvalidRuleMode = errors.New("invalid screening rule or mode")
