// Package privacyexport defines the bounded cursor contract shared by every
// privacy-data owner and the auth coordinator.
package privacyexport

import (
	"fmt"
	"net/url"
	"strconv"
)

const (
	DefaultPageSize = 100
	MaxPageSize     = 500
)

func Parse(values url.Values) (offset, pageSize int, err error) {
	pageSize = DefaultPageSize
	if raw := values.Get("page_size"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > MaxPageSize {
			return 0, 0, fmt.Errorf("page_size must be between 1 and %d", MaxPageSize)
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("invalid cursor")
		}
	}
	return offset, pageSize, nil
}

func Next(offset, returned int, hasMore bool) string {
	if !hasMore {
		return ""
	}
	return strconv.Itoa(offset + returned)
}
