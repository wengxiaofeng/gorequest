package gorequest

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRetryJsonPathTransientResponses(t *testing.T) {
	for _, failure := range []string{"empty", "html", "429", "503", "515", "business"} {
		t.Run(failure, func(t *testing.T) {
			calls := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := ioutil.ReadAll(r.Body)
				if r.Method != "POST" || string(raw) != `{"version":"8.2"}` {
					t.Errorf("request changed: %s %s", r.Method, raw)
				}
				if calls == 1 {
					switch failure {
					case "empty":
						return
					case "html":
						fmt.Fprint(w, "Internal Server Error")
					case "429":
						w.WriteHeader(429)
						fmt.Fprint(w, `{"err_msg":""}`)
					case "503":
						w.WriteHeader(503)
						fmt.Fprint(w, `{"err_msg":""}`)
					case "515":
						w.WriteHeader(515)
					case "business":
						fmt.Fprint(w, `{"err_msg":"busy"}`)
					}
					return
				}
				fmt.Fprint(w, `{"err_msg":"","value":42}`)
			}))
			defer ts.Close()
			var out struct {
				Value int `json:"value"`
			}
			resp, body, errs := New().Post(ts.URL).Send(`{"version":"8.2"}`).RetryJsonPath(3, 0, "$.err_msg", "", false).EndStruct(&out)
			if len(errs) > 0 || calls != 2 || out.Value != 42 || resp.Header.Get("Retry-Count") != "1" {
				t.Fatalf("calls=%d out=%+v errs=%v", calls, out, errs)
			}
			remaining, _ := ioutil.ReadAll(resp.Body)
			if string(remaining) != string(body) {
				t.Fatalf("response body consumed: %q", remaining)
			}
		})
	}
}

func TestRetryJsonPathCompatibilityAndLimit(t *testing.T) {
	for _, tc := range []struct {
		name, body             string
		status, retries, calls int
		enabled                bool
	}{
		{"missing path", `{"value":42}`, 200, 3, 1, true},
		{"disabled", "", 200, 3, 1, false},
		{"zero retries", "", 200, 0, 1, true},
		{"exhausted", "", 200, 2, 3, true},
		{"unauthorized", "", 401, 3, 1, true},
		{"forbidden", "", 403, 3, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-Tt-Logid", "trace-1")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer ts.Close()
			req := New().Get(ts.URL)
			if tc.enabled {
				req.RetryJsonPath(tc.retries, 0, "$.err_msg", "", false)
			}
			var out interface{}
			_, _, errs := req.EndStruct(&out)
			if calls != tc.calls {
				t.Fatalf("calls=%d want=%d", calls, tc.calls)
			}
			if tc.body == "" && (len(errs) == 0 || !strings.Contains(errs[0].Error(), "logid:trace-1")) {
				t.Fatalf("missing parse diagnostic: %v", errs)
			}
		})
	}
}

func TestRetryJsonPathTransportTimeout(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"err_msg":""}`)
	}))
	defer ts.Close()
	_, _, errs := New().Get(ts.URL).Timeout(50*time.Millisecond).RetryJsonPath(1, 0, "$.err_msg", "", false).EndBytes()
	if len(errs) > 0 || calls != 2 {
		t.Fatalf("calls=%d errs=%v", calls, errs)
	}
}

func TestRetryJsonPathCompositeValue(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"value":[1]}`)
		} else {
			fmt.Fprint(w, `{"value":[]}`)
		}
	}))
	defer ts.Close()
	_, _, errs := New().Get(ts.URL).RetryJsonPath(1, 0, "$.value", []interface{}{float64(1)}, true).EndBytes()
	if len(errs) > 0 || calls != 2 {
		t.Fatalf("calls=%d errs=%v", calls, errs)
	}
}
