/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newExternalSync returns a baseline-valid ExternalAttributeSync with the
// supplied spec overlay applied. Tests mutate the returned value to
// exercise specific rules without restating the full struct each time.
func newExternalSync(name string, spec ExternalAttributeSyncSpec) *ExternalAttributeSync {
	if spec.Schedule == "" {
		spec.Schedule = "5m"
	}
	if spec.Source.Kind == "" {
		spec.Source = ExternalSource{
			Kind: ExternalSourceKindHTTP,
			HTTP: &HTTPSourceSpec{
				URL:    "https://example.com/records",
				Method: "GET",
			},
		}
	}
	if spec.Mapping.CollectorIDField == "" {
		spec.Mapping.CollectorIDField = "collector_id"
	}
	if spec.Mapping.AttributeFields == nil {
		spec.Mapping.AttributeFields = map[string]string{
			"team": "team_field",
		}
	}
	if len(spec.Selector.Matchers) == 0 && len(spec.Selector.CollectorIDs) == 0 {
		spec.Selector.Matchers = []string{"environment=production"}
	}
	return &ExternalAttributeSync{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
}

func TestExternalAttributeSync_ValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		eas     *ExternalAttributeSync
		wantErr bool
		errMsg  string
	}{
		// --- Rule 1: schedule required and parses as duration or cron. ---
		{
			name: "valid - 5m duration schedule",
			eas: newExternalSync("ok-duration", ExternalAttributeSyncSpec{
				Schedule: "5m",
			}),
			wantErr: false,
		},
		{
			name: "valid - 30s duration schedule",
			eas: newExternalSync("ok-30s", ExternalAttributeSyncSpec{
				Schedule: "30s",
			}),
			wantErr: false,
		},
		{
			name: "valid - cron expression every 15 minutes",
			eas: newExternalSync("ok-cron", ExternalAttributeSyncSpec{
				Schedule: "*/15 * * * *",
			}),
			wantErr: false,
		},
		{
			name: "valid - daily cron at 02:30",
			eas: newExternalSync("ok-cron-daily", ExternalAttributeSyncSpec{
				Schedule: "30 2 * * *",
			}),
			wantErr: false,
		},
		{
			name: "invalid - empty schedule",
			eas: &ExternalAttributeSync{
				ObjectMeta: metav1.ObjectMeta{Name: "no-schedule", Namespace: "default"},
				Spec: ExternalAttributeSyncSpec{
					Schedule: "",
					Source: ExternalSource{
						Kind: ExternalSourceKindHTTP,
						HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
					},
					Mapping: AttributeMapping{
						CollectorIDField: "collector_id",
						AttributeFields:  map[string]string{"team": "team_field"},
					},
					Selector: PolicySelector{Matchers: []string{"environment=production"}},
				},
			},
			wantErr: true,
			errMsg:  "spec.schedule is required",
		},
		{
			name: "invalid - whitespace-only schedule",
			eas: newExternalSync("ws-schedule", ExternalAttributeSyncSpec{
				Schedule: "   ",
			}),
			wantErr: true,
			errMsg:  "spec.schedule is required",
		},
		{
			name: "invalid - malformed schedule reports both parse errors",
			eas: newExternalSync("bad-schedule", ExternalAttributeSyncSpec{
				Schedule: "every other tuesday",
			}),
			wantErr: true,
			errMsg:  "neither a valid Go duration nor a valid 5-field cron expression",
		},
		{
			name: "invalid - malformed schedule includes duration error",
			eas: newExternalSync("bad-dur", ExternalAttributeSyncSpec{
				Schedule: "5x",
			}),
			wantErr: true,
			errMsg:  "duration parse error",
		},
		{
			name: "invalid - malformed schedule includes cron error",
			eas: newExternalSync("bad-cron", ExternalAttributeSyncSpec{
				Schedule: "5x",
			}),
			wantErr: true,
			errMsg:  "cron parse error",
		},
		{
			name: "invalid - 6-field cron rejected by 5-field parser",
			eas: newExternalSync("six-field", ExternalAttributeSyncSpec{
				Schedule: "* * * * * *",
			}),
			wantErr: true,
			errMsg:  "neither a valid Go duration nor a valid 5-field cron expression",
		},

		// --- Rule 2: source.kind enum (defense-in-depth). ---
		{
			name: "invalid - source kind is unknown value",
			eas: newExternalSync("bad-kind", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKind("FTP"),
					HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
				},
			}),
			wantErr: true,
			errMsg:  "spec.source.kind",
		},

		// --- Rule 3: source kind/spec consistency. ---
		{
			name: "valid - HTTP source with http spec set, sql nil",
			eas: newExternalSync("ok-http", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
				},
			}),
			wantErr: false,
		},
		{
			name: "valid - SQL source with sql spec set, http nil",
			eas: newExternalSync("ok-sql", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindSQL,
					SQL:  &SQLSourceSpec{Driver: "postgres", Query: "SELECT id FROM t"},
				},
			}),
			wantErr: false,
		},
		{
			name: "invalid - HTTP kind with nil http spec",
			eas: newExternalSync("http-nil", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
				},
			}),
			wantErr: true,
			errMsg:  "spec.source.kind=HTTP requires spec.source.http",
		},
		{
			name: "invalid - HTTP kind with sql spec also set",
			eas: newExternalSync("http-and-sql", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
					SQL:  &SQLSourceSpec{Driver: "postgres"},
				},
			}),
			wantErr: true,
			errMsg:  "must not also set spec.source.sql",
		},
		{
			name: "invalid - SQL kind with nil sql spec",
			eas: newExternalSync("sql-nil", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindSQL,
				},
			}),
			wantErr: true,
			errMsg:  "spec.source.kind=SQL requires spec.source.sql",
		},
		{
			name: "invalid - SQL kind with http spec also set",
			eas: newExternalSync("sql-and-http", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindSQL,
					SQL:  &SQLSourceSpec{Driver: "postgres"},
					HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
				},
			}),
			wantErr: true,
			errMsg:  "must not also set spec.source.http",
		},

		// --- Rule 4: HTTP URL parses, has scheme http or https. ---
		{
			name: "valid - http scheme accepted",
			eas: newExternalSync("ok-http-scheme", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "http://internal.example.com/records"},
				},
			}),
			wantErr: false,
		},
		{
			name: "invalid - HTTP URL is whitespace-only",
			eas: newExternalSync("ws-url", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "   "},
				},
			}),
			wantErr: true,
			errMsg:  "spec.source.http.url is required",
		},
		{
			name: "invalid - HTTP URL with ftp scheme",
			eas: newExternalSync("ftp-scheme", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "ftp://example.com/records"},
				},
			}),
			wantErr: true,
			errMsg:  "must be http or https",
		},
		{
			name: "invalid - HTTP URL missing scheme",
			eas: newExternalSync("no-scheme", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "example.com/records"},
				},
			}),
			wantErr: true,
			errMsg:  "must be http or https",
		},
		{
			name: "invalid - HTTP URL with no host",
			eas: newExternalSync("no-host", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "http:///records"},
				},
			}),
			wantErr: true,
			errMsg:  "missing a host component",
		},
		{
			name: "invalid - HTTP URL is unparseable garbage",
			eas: newExternalSync("garbage-url", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "ht!tp://[bad"},
				},
			}),
			wantErr: true,
			// url.Parse may classify either as parse failure or scheme issue
			// depending on Go version; both messages are valid.
			errMsg: "spec.source.http.url",
		},

		// --- Rule 5: HTTP method must be GET or POST. ---
		{
			name: "valid - empty method (defaults to GET)",
			eas: newExternalSync("ok-empty-method", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "https://example.com/records", Method: ""},
				},
			}),
			wantErr: false,
		},
		{
			name: "valid - POST method",
			eas: newExternalSync("ok-post", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "https://example.com/records", Method: "POST"},
				},
			}),
			wantErr: false,
		},
		{
			name: "invalid - PUT method",
			eas: newExternalSync("bad-put", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "https://example.com/records", Method: "PUT"},
				},
			}),
			wantErr: true,
			errMsg:  "must be GET or POST",
		},
		{
			name: "invalid - lowercase get is not accepted",
			eas: newExternalSync("bad-lower-get", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "https://example.com/records", Method: "get"},
				},
			}),
			wantErr: true,
			errMsg:  "must be GET or POST",
		},

		// --- Rule 6: collectorIDField non-empty after trim. ---
		{
			name: "invalid - empty collectorIDField",
			eas: &ExternalAttributeSync{
				ObjectMeta: metav1.ObjectMeta{Name: "empty-cid-field", Namespace: "default"},
				Spec: ExternalAttributeSyncSpec{
					Schedule: "5m",
					Source: ExternalSource{
						Kind: ExternalSourceKindHTTP,
						HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
					},
					Mapping: AttributeMapping{
						CollectorIDField: "",
						AttributeFields:  map[string]string{"team": "team_field"},
					},
					Selector: PolicySelector{Matchers: []string{"environment=production"}},
				},
			},
			wantErr: true,
			errMsg:  "spec.mapping.collectorIDField is required",
		},
		{
			name: "invalid - whitespace-only collectorIDField",
			eas: newExternalSync("ws-cid-field", ExternalAttributeSyncSpec{
				Mapping: AttributeMapping{
					CollectorIDField: "   ",
					AttributeFields:  map[string]string{"team": "team_field"},
				},
			}),
			wantErr: true,
			errMsg:  "spec.mapping.collectorIDField is required",
		},

		// --- Rule 7: attributeFields non-empty. ---
		{
			name: "invalid - attributeFields empty map",
			eas: newExternalSync("empty-fields", ExternalAttributeSyncSpec{
				Mapping: AttributeMapping{
					CollectorIDField: "collector_id",
					AttributeFields:  map[string]string{},
				},
			}),
			wantErr: true,
			errMsg:  "spec.mapping.attributeFields must contain at least one entry",
		},
		{
			name: "invalid - attributeFields nil",
			eas: &ExternalAttributeSync{
				ObjectMeta: metav1.ObjectMeta{Name: "nil-fields", Namespace: "default"},
				Spec: ExternalAttributeSyncSpec{
					Schedule: "5m",
					Source: ExternalSource{
						Kind: ExternalSourceKindHTTP,
						HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
					},
					Mapping: AttributeMapping{
						CollectorIDField: "collector_id",
					},
					Selector: PolicySelector{Matchers: []string{"environment=production"}},
				},
			},
			wantErr: true,
			errMsg:  "spec.mapping.attributeFields must contain at least one entry",
		},

		// --- Rule 8: attribute keys must not use reserved prefix. ---
		{
			name: "invalid - reserved prefix collector.os in mapping",
			eas: newExternalSync("reserved-os", ExternalAttributeSyncSpec{
				Mapping: AttributeMapping{
					CollectorIDField: "collector_id",
					AttributeFields:  map[string]string{"collector.os": "os_field"},
				},
			}),
			wantErr: true,
			errMsg:  "reserved prefix",
		},
		{
			name: "invalid - reserved prefix collector.custom.key in mapping",
			eas: newExternalSync("reserved-custom", ExternalAttributeSyncSpec{
				Mapping: AttributeMapping{
					CollectorIDField: "collector_id",
					AttributeFields:  map[string]string{"collector.custom.key": "field"},
				},
			}),
			wantErr: true,
			errMsg:  `"collector.custom.key"`,
		},
		{
			name: "invalid - empty key in attributeFields",
			eas: newExternalSync("empty-key", ExternalAttributeSyncSpec{
				Mapping: AttributeMapping{
					CollectorIDField: "collector_id",
					AttributeFields:  map[string]string{"": "team_field"},
				},
			}),
			wantErr: true,
			errMsg:  "empty key",
		},
		{
			name: "valid - case-sensitive non-reserved prefix",
			eas: newExternalSync("similar-prefix", ExternalAttributeSyncSpec{
				Mapping: AttributeMapping{
					CollectorIDField: "collector_id",
					// Capital "C" — not reserved. Plural "collectors." — not reserved.
					AttributeFields: map[string]string{
						"Collector.os":    "os_field",
						"collectors.team": "team_field",
					},
				},
			}),
			wantErr: false,
		},

		// --- Rule 9: selector non-empty. ---
		{
			name: "valid - selector with collectorIDs only",
			eas: newExternalSync("ok-cids", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					CollectorIDs: []string{"collector-1", "collector-2"},
				},
			}),
			wantErr: false,
		},
		{
			name: "valid - selector with both matchers and collectorIDs",
			eas: newExternalSync("ok-both", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					Matchers:     []string{"environment=production"},
					CollectorIDs: []string{"collector-1"},
				},
			}),
			wantErr: false,
		},
		{
			name: "invalid - selector empty (no matchers, no collectorIDs)",
			eas: &ExternalAttributeSync{
				ObjectMeta: metav1.ObjectMeta{Name: "empty-selector", Namespace: "default"},
				Spec: ExternalAttributeSyncSpec{
					Schedule: "5m",
					Source: ExternalSource{
						Kind: ExternalSourceKindHTTP,
						HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
					},
					Mapping: AttributeMapping{
						CollectorIDField: "collector_id",
						AttributeFields:  map[string]string{"team": "team_field"},
					},
					Selector: PolicySelector{},
				},
			},
			wantErr: true,
			errMsg:  "spec.selector must specify at least one of matchers or collectorIDs",
		},

		// --- Rule 10: matcher syntax + 200-char cap. ---
		{
			name: "valid - regex matcher",
			eas: newExternalSync("matcher-regex", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					Matchers: []string{"team=~team-(a|b)"},
				},
			}),
			wantErr: false,
		},
		{
			name: "invalid - matcher uses double equals",
			eas: newExternalSync("matcher-double-equals", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					Matchers: []string{"environment==production"},
				},
			}),
			wantErr: true,
			errMsg:  "spec.selector.matchers[0]",
		},
		{
			name: "invalid - matcher missing operator",
			eas: newExternalSync("matcher-no-op", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					Matchers: []string{"environment"},
				},
			}),
			wantErr: true,
			errMsg:  "invalid Prometheus matcher syntax",
		},
		{
			name: "invalid - matcher exceeds 200 character cap",
			eas: newExternalSync("matcher-too-long", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					Matchers: []string{"environment=" + strings.Repeat("a", 200)},
				},
			}),
			wantErr: true,
			errMsg:  "exceeds 200 character limit",
		},

		// --- Rule 11: collectorIDs entries non-empty after trim. ---
		{
			name: "invalid - collectorIDs contains empty string",
			eas: newExternalSync("empty-cid", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					CollectorIDs: []string{"collector-1", ""},
				},
			}),
			wantErr: true,
			errMsg:  "spec.selector.collectorIDs[1]",
		},
		{
			name: "invalid - collectorIDs contains whitespace-only entry",
			eas: newExternalSync("ws-cid", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					CollectorIDs: []string{"   "},
				},
			}),
			wantErr: true,
			errMsg:  "must be non-empty",
		},

		// --- AllowEmptyResults is mutable, no extra validation. ---
		{
			name: "valid - allowEmptyResults set",
			eas: newExternalSync("with-allow-empty", ExternalAttributeSyncSpec{
				AllowEmptyResults: true,
			}),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := (&externalAttributeSyncValidator{}).ValidateCreate(ctx, tt.eas)
			if tt.wantErr {
				if assert.Error(t, err) && tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestExternalAttributeSync_ValidateUpdate(t *testing.T) {
	baseOld := newExternalSync("test", ExternalAttributeSyncSpec{
		Schedule: "5m",
	})

	tests := []struct {
		name    string
		oldObj  *ExternalAttributeSync
		newObj  *ExternalAttributeSync
		wantErr bool
		errMsg  string
	}{
		{
			name:   "valid - schedule updated to cron (schedule is mutable)",
			oldObj: baseOld,
			newObj: newExternalSync("test", ExternalAttributeSyncSpec{
				Schedule: "*/15 * * * *",
			}),
			wantErr: false,
		},
		{
			name:   "valid - source kind switched HTTP -> SQL (all fields mutable)",
			oldObj: baseOld,
			newObj: newExternalSync("test", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindSQL,
					SQL:  &SQLSourceSpec{Driver: "postgres", Query: "SELECT 1"},
				},
			}),
			wantErr: false,
		},
		{
			name:   "valid - selector updated",
			oldObj: baseOld,
			newObj: newExternalSync("test", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					CollectorIDs: []string{"collector-77"},
				},
			}),
			wantErr: false,
		},
		{
			name:   "invalid - update introduces malformed schedule",
			oldObj: baseOld,
			newObj: newExternalSync("test", ExternalAttributeSyncSpec{
				Schedule: "every other tuesday",
			}),
			wantErr: true,
			errMsg:  "neither a valid Go duration nor a valid 5-field cron expression",
		},
		{
			name:   "invalid - update flips kind to HTTP but leaves http nil",
			oldObj: baseOld,
			newObj: &ExternalAttributeSync{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec: ExternalAttributeSyncSpec{
					Schedule: "5m",
					Source: ExternalSource{
						Kind: ExternalSourceKindHTTP,
					},
					Mapping: AttributeMapping{
						CollectorIDField: "collector_id",
						AttributeFields:  map[string]string{"team": "team_field"},
					},
					Selector: PolicySelector{Matchers: []string{"environment=production"}},
				},
			},
			wantErr: true,
			errMsg:  "spec.source.kind=HTTP requires spec.source.http",
		},
		{
			name:   "invalid - update sets both http and sql under HTTP kind",
			oldObj: baseOld,
			newObj: newExternalSync("test", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
					SQL:  &SQLSourceSpec{Driver: "postgres"},
				},
			}),
			wantErr: true,
			errMsg:  "must not also set spec.source.sql",
		},
		{
			name:   "invalid - update introduces reserved attribute prefix",
			oldObj: baseOld,
			newObj: newExternalSync("test", ExternalAttributeSyncSpec{
				Mapping: AttributeMapping{
					CollectorIDField: "collector_id",
					AttributeFields: map[string]string{
						"team":         "team_field",
						"collector.os": "os_field",
					},
				},
			}),
			wantErr: true,
			errMsg:  "reserved prefix",
		},
		{
			name:   "invalid - update empties attributeFields",
			oldObj: baseOld,
			newObj: &ExternalAttributeSync{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec: ExternalAttributeSyncSpec{
					Schedule: "5m",
					Source: ExternalSource{
						Kind: ExternalSourceKindHTTP,
						HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
					},
					Mapping: AttributeMapping{
						CollectorIDField: "collector_id",
						AttributeFields:  map[string]string{},
					},
					Selector: PolicySelector{Matchers: []string{"environment=production"}},
				},
			},
			wantErr: true,
			errMsg:  "spec.mapping.attributeFields must contain at least one entry",
		},
		{
			name:   "invalid - update empties selector",
			oldObj: baseOld,
			newObj: &ExternalAttributeSync{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec: ExternalAttributeSyncSpec{
					Schedule: "5m",
					Source: ExternalSource{
						Kind: ExternalSourceKindHTTP,
						HTTP: &HTTPSourceSpec{URL: "https://example.com/records"},
					},
					Mapping: AttributeMapping{
						CollectorIDField: "collector_id",
						AttributeFields:  map[string]string{"team": "team_field"},
					},
					Selector: PolicySelector{},
				},
			},
			wantErr: true,
			errMsg:  "spec.selector must specify at least one of matchers or collectorIDs",
		},
		{
			name:   "invalid - update introduces invalid matcher syntax",
			oldObj: baseOld,
			newObj: newExternalSync("test", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					Matchers: []string{"environment==production"},
				},
			}),
			wantErr: true,
			errMsg:  "spec.selector.matchers[0]",
		},
		{
			name:   "invalid - update introduces empty collectorID entry",
			oldObj: baseOld,
			newObj: newExternalSync("test", ExternalAttributeSyncSpec{
				Selector: PolicySelector{
					CollectorIDs: []string{"collector-1", ""},
				},
			}),
			wantErr: true,
			errMsg:  "spec.selector.collectorIDs[1]",
		},
		{
			name:   "invalid - update sets HTTP method to PUT",
			oldObj: baseOld,
			newObj: newExternalSync("test", ExternalAttributeSyncSpec{
				Source: ExternalSource{
					Kind: ExternalSourceKindHTTP,
					HTTP: &HTTPSourceSpec{URL: "https://example.com/records", Method: "PUT"},
				},
			}),
			wantErr: true,
			errMsg:  "must be GET or POST",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := (&externalAttributeSyncValidator{}).ValidateUpdate(ctx, tt.oldObj, tt.newObj)
			if tt.wantErr {
				if assert.Error(t, err) && tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestExternalAttributeSync_ValidateDelete(t *testing.T) {
	eas := newExternalSync("test", ExternalAttributeSyncSpec{})
	warnings, err := (&externalAttributeSyncValidator{}).ValidateDelete(context.Background(), eas)
	assert.NoError(t, err)
	assert.Nil(t, warnings)
}
