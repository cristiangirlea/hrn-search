package task

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

const (
	MaxExamples   = 1000
	MaxConstants  = 16
	MaxDepth      = 5
	MaxCandidates = 5_000_000
)

type Example struct {
	X   int64 `json:"x"`
	Y   int64 `json:"y"`
	Out int64 `json:"out"`
}

type Spec struct {
	Training      []Example `json:"training"`
	Validation    []Example `json:"validation"`
	Constants     []int64   `json:"constants,omitempty"`
	MaxDepth      int       `json:"max_depth"`
	MaxCandidates int       `json:"max_candidates"`
}

func (s Spec) Validate() error {
	switch {
	case len(s.Training) < 1 || len(s.Training) > MaxExamples:
		return fmt.Errorf("training must have 1 to %d examples", MaxExamples)
	case len(s.Validation) < 1 || len(s.Validation) > MaxExamples:
		return fmt.Errorf("validation must have 1 to %d examples", MaxExamples)
	case len(s.Constants) > MaxConstants:
		return fmt.Errorf("constants exceeds %d entries", MaxConstants)
	case s.MaxDepth < 0 || s.MaxDepth > MaxDepth:
		return fmt.Errorf("max_depth must be 0 to %d", MaxDepth)
	case s.MaxCandidates < 1 || s.MaxCandidates > MaxCandidates:
		return fmt.Errorf("max_candidates must be 1 to %d", MaxCandidates)
	}
	for i, c := range s.Constants {
		if slices.Contains(s.Constants[:i], c) {
			return fmt.Errorf("constants[%d] repeats %d", i, c)
		}
	}
	return nil
}

func Decode(raw []byte) (Spec, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	s := Spec{MaxDepth: 3, MaxCandidates: 100_000}
	err := object(d, "input", func(key string) error {
		var err error
		switch key {
		case "training":
			s.Training, err = list(d, key, MaxExamples, example)
		case "validation":
			s.Validation, err = list(d, key, MaxExamples, example)
		case "constants":
			s.Constants, err = list(d, key, MaxConstants, integer)
		case "max_depth", "max_candidates":
			n, err := integer(d, key)
			if err != nil {
				return err
			}
			if n != int64(int(n)) {
				return fmt.Errorf("%s is outside its range", key)
			}
			if key == "max_depth" {
				s.MaxDepth = int(n)
			} else {
				s.MaxCandidates = int(n)
			}
		default:
			return fmt.Errorf("unknown input field %.100q", key)
		}
		return err
	})
	if err != nil {
		return Spec{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return Spec{}, fmt.Errorf("input has trailing data")
	}
	if err := s.Validate(); err != nil {
		return Spec{}, err
	}
	return s, nil
}

// Token decoding preserves key order and exposes duplicates before assigning a value.
func object(d *json.Decoder, path string, field func(string) error) error {
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return fmt.Errorf("%s must be an object", path)
	}
	seen := map[string]bool{}
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("%s needs a field name", path)
		}
		if seen[key] {
			return fmt.Errorf("%s repeats field %.100q", path, key)
		}
		seen[key] = true
		if err := field(key); err != nil {
			return err
		}
	}
	if tok, err := d.Token(); err != nil || tok != json.Delim('}') {
		return fmt.Errorf("%s needs a closing brace", path)
	}
	return nil
}

func list[T any](d *json.Decoder, path string, limit int, item func(*json.Decoder, string) (T, error)) ([]T, error) {
	if tok, err := d.Token(); err != nil || tok != json.Delim('[') {
		return nil, fmt.Errorf("%s must be an array", path)
	}
	var items []T
	for d.More() {
		if len(items) == limit {
			return nil, fmt.Errorf("%s exceeds %d entries", path, limit)
		}
		value, err := item(d, fmt.Sprintf("%s[%d]", path, len(items)))
		if err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	if tok, err := d.Token(); err != nil || tok != json.Delim(']') {
		return nil, fmt.Errorf("%s needs a closing bracket", path)
	}
	return items, nil
}

func example(d *json.Decoder, path string) (Example, error) {
	var row Example
	fields := map[string]*int64{"x": &row.X, "y": &row.Y, "out": &row.Out}
	err := object(d, path, func(key string) error {
		dst := fields[key]
		if dst == nil {
			return fmt.Errorf("%s has unknown field %.100q", path, key)
		}
		n, err := integer(d, path+"."+key)
		if err != nil {
			return err
		}
		*dst = n
		delete(fields, key)
		return nil
	})
	if err != nil {
		return Example{}, err
	}
	for _, key := range []string{"x", "y", "out"} {
		if fields[key] != nil {
			return Example{}, fmt.Errorf("%s.%s is required", path, key)
		}
	}
	return row, nil
}

func integer(d *json.Decoder, path string) (int64, error) {
	tok, err := d.Token()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	if n, ok := tok.(json.Number); ok {
		if v, ok := exactInteger(n.String()); ok {
			return v, nil
		}
	}
	return 0, fmt.Errorf("%s must be an exact signed 64-bit integer", path)
}

// Decimal shifts avoid floating-point rounding and allocation proportional to the exponent.
func exactInteger(s string) (int64, bool) {
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v, true
	}
	mantissa, exponent := s, "0"
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa, exponent = s[:i], s[i+1:]
	}
	mantissa, negative := strings.CutPrefix(mantissa, "-")
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return 0, true
	}
	exp, err := strconv.ParseInt(exponent, 10, 32)
	if err != nil {
		return 0, false
	}
	significant := strings.TrimRight(digits, "0")
	shift := exp - int64(len(fraction)) + int64(len(digits)-len(significant))
	if shift < 0 || int64(len(significant))+shift > 19 {
		return 0, false
	}
	s = significant + strings.Repeat("0", int(shift))
	if negative {
		s = "-" + s
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}
