package request_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaleCoopTP/grishin_lab1/internal/http/request"
)

const jsonType = "application/json"

type payload struct {
	Name  string `json:"name"`
	Point struct {
		X *float64 `json:"x"`
	} `json:"point"`
}

func TestDecodeJSON(t *testing.T) {
	cases := []struct {
		name, contentType, body string
		ok                      bool
	}{
		{"valid", jsonType, `{"name":"a","point":{"x":1}}`, true},
		{"charset parameter", jsonType + "; charset=utf-8", `{"name":"a","point":{"x":0}}`, true},
		{"wrong content type", "text/plain", `{"name":"a","point":{"x":1}}`, false},
		{"not an object", jsonType, `[]`, false},
		{"missing field", jsonType, `{"point":{"x":1}}`, false},
		{"null field", jsonType, `{"name":null,"point":{"x":1}}`, false},
		{"missing nested field", jsonType, `{"name":"a","point":{}}`, false},
		{"unknown field", jsonType, `{"name":"a","point":{"x":1},"extra":1}`, false},
		{"two objects", jsonType, `{"name":"a","point":{"x":1}} {}`, false},
		{"wrong type", jsonType, `{"name":1,"point":{"x":1}}`, false},
		{"too large", jsonType, `{"name":"` + strings.Repeat("a", 1<<20) + `","point":{"x":1}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			var got payload
			fields, err := request.DecodeJSON(httptest.NewRecorder(), req, &got, "name", "point")
			if err == nil {
				err = request.RequireFields(fields, "point", "x")
			}
			if (err == nil) != tc.ok {
				t.Fatalf("got error %v, want ok=%v", err, tc.ok)
			}
			if tc.ok && (got.Name != "a" || got.Point.X == nil) {
				t.Fatalf("decoded %+v", got)
			}
		})
	}
}
