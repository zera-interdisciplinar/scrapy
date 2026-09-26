// Package eval resolves an entry's value for a given mobile user, applying segmentation
// rules server-side so the client never sees rules or other segments' data.
package eval

import (
	"encoding/json"
	"strconv"
)

type Rule struct {
	When []Cond          `json:"when"`
	Then json.RawMessage `json:"then"`
}

type Cond struct {
	Attr  string      `json:"attr"`
	Op    string      `json:"op"` // eq, neq, gte, lte, in
	Value interface{} `json:"value"`
}

// Resolve returns the first rule whose conditions all match attrs, else the base value.
func Resolve(baseValue json.RawMessage, rulesJSON json.RawMessage, attrs map[string]interface{}) json.RawMessage {
	var rules []Rule
	if len(rulesJSON) > 0 {
		_ = json.Unmarshal(rulesJSON, &rules)
	}
	for _, r := range rules {
		if matches(r.When, attrs) {
			return r.Then
		}
	}
	return baseValue
}

func matches(conds []Cond, attrs map[string]interface{}) bool {
	for _, c := range conds {
		got, ok := attrs[c.Attr]
		if !ok {
			return false
		}
		if !compare(got, c.Op, c.Value) {
			return false
		}
	}
	return true
}

func compare(got interface{}, op string, want interface{}) bool {
	switch op {
	case "eq":
		return toStr(got) == toStr(want)
	case "neq":
		return toStr(got) != toStr(want)
	case "in":
		list, ok := want.([]interface{})
		if !ok {
			return false
		}
		for _, v := range list {
			if toStr(v) == toStr(got) {
				return true
			}
		}
		return false
	case "gte":
		g, w := toFloat(got), toFloat(want)
		return g >= w
	case "lte":
		g, w := toFloat(got), toFloat(want)
		return g <= w
	default:
		return false
	}
}

func toStr(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func toFloat(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}
