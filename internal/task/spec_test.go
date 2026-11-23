package task

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

const row = `{"x":1,"y":2,"out":3}`
const minimal = `{"training":[` + row + `],"validation":[` + row + `]}`

func TestDecodedTasksRoundTripWithExactIntegersAndDefaults(t *testing.T) {
	input := strings.Replace(minimal, row, `{"out":-92233720368547758080e-1,"y":9.223372036854775807e18,"x":9007199254740993}`, 1)
	s, err := Decode([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if s.Training[0] != (Example{9007199254740993, math.MaxInt64, math.MinInt64}) || s.MaxDepth != 3 || s.MaxCandidates != 100000 {
		t.Fatalf("decoded %+v", s)
	}
	for _, input := range []string{input, minimal[:len(minimal)-1] + `,"constants":[2,1],"max_depth":0,"max_candidates":5000000}`} {
		s, err := Decode([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(raw)
		if err != nil || !reflect.DeepEqual(got, s) {
			t.Fatalf("round trip %s: %+v, %v", raw, got, err)
		}
	}
}

func TestInvalidTasksIdentifyTheOffendingField(t *testing.T) {
	add := func(fields string) string { return minimal[:len(minimal)-1] + "," + fields + "}" }
	replaceRow := func(value string) string { return strings.Replace(minimal, row, value, 1) }
	many := strings.Repeat(row+",", 1000) + row
	for _, tc := range []struct{ input, field string }{
		{"", "input"}, {"[]", "input"}, {minimal + " {}", "input"},
		{`{"validation":[` + row + `]}`, "training"}, {`{"training":[` + row + `]}`, "validation"},
		{strings.Replace(minimal, "["+row+"]", "[]", 1), "training"},
		{strings.ReplaceAll(minimal, `"validation":[`+row+`]`, `"validation":[]`), "validation"},
		{replaceRow(many), "training"}, {strings.Replace(minimal, `"validation":[`+row, `"validation":[`+many, 1), "validation"},
		{add(`"typo":true`), "typo"}, {add(`"Max_depth":2`), "Max_depth"},
		{add(`"training":[]`), "training"}, {add(`"max_depth":0,"max_depth":1`), "max_depth"},
		{replaceRow(`{"x":1,"y":2,"out":3,"extra":4}`), "extra"},
		{replaceRow(`{"x":0,"\u0078":1,"y":2,"out":3}`), "x"},
		{replaceRow(`{"X":0,"y":2,"out":3}`), "X"},
		{replaceRow("null"), "training[0]"}, {replaceRow(`{"y":2,"out":3}`), "x"},
		{replaceRow(`{"x":1,"out":3}`), "y"}, {replaceRow(`{"x":1,"y":2}`), "out"},
		{replaceRow(`{"x":9223372036854775808,"y":2,"out":3}`), "x"},
		{replaceRow(`{"x":1,"y":-9223372036854775809,"out":3}`), "y"},
		{replaceRow(`{"x":1,"y":2,"out":9.1}`), "out"},
		{strings.Replace(minimal, "["+row+"]", "null", 1), "training"},
		{add(`"constants":null`), "constants"}, {add(`"constants":{}`), "constants"},
		{add(`"constants":[1,10e-1]`), "constants"},
		{add(`"constants":[0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]`), "constants"},
		{add(`"constants":[1e-1]`), "constants[0]"}, {add(`"constants":[1e999999999999999999999]`), "constants[0]"},
		{add(`"max_depth":null`), "max_depth"}, {add(`"max_depth":-1`), "max_depth"},
		{add(`"max_depth":6`), "max_depth"}, {add(`"max_candidates":0`), "max_candidates"},
		{add(`"max_candidates":5000001`), "max_candidates"}, {add(`"max_candidates":"1"`), "max_candidates"},
	} {
		_, err := Decode([]byte(tc.input))
		if err == nil || !strings.Contains(err.Error(), tc.field) {
			t.Errorf("expected error naming %q, got %v", tc.field, err)
		}
	}
}
