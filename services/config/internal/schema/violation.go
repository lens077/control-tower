package schema

import (
	"errors"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Violation 只保留实例位置。Bootstrap 可能包含凭据，因此主动丢弃 Schema 消息。
type Violation struct {
	locations []string
}

func newViolation(err error) *Violation {
	locations := validationLocations(err)
	if len(locations) == 0 {
		locations = []string{"/"}
	}
	return &Violation{locations: locations}
}

func (e *Violation) Error() string {
	return "invalid configuration at " + strings.Join(e.locations, ", ")
}

func (e *Violation) Locations() []string {
	return append([]string(nil), e.locations...)
}

func validationLocations(err error) []string {
	var validationErr *jsonschema.ValidationError
	if !errors.As(err, &validationErr) {
		return nil
	}

	unique := make(map[string]struct{})
	var walk func(*jsonschema.ValidationError)
	walk = func(current *jsonschema.ValidationError) {
		if len(current.Causes) == 0 {
			location := current.InstanceLocation
			if location == "" {
				location = "/"
			}
			unique[location] = struct{}{}
			return
		}
		for _, cause := range current.Causes {
			walk(cause)
		}
	}
	walk(validationErr)

	locations := make([]string, 0, len(unique))
	for location := range unique {
		locations = append(locations, location)
	}
	sort.Strings(locations)
	return locations
}
