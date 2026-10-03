package events

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fixedClock returns a deterministic, strictly increasing sequence of instants so
// ordering assertions never depend on wall-clock time.
type fixedClock struct {
	base  time.Time
	calls int
}

func (c *fixedClock) now() time.Time {
	instant := c.base.Add(time.Duration(c.calls) * time.Second)
	c.calls++
	return instant
}

func newTestPublisher(t *testing.T) *Publisher {
	t.Helper()
	return NewPublisher(
		WithClock((&fixedClock{base: time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)}).now),
		WithIDMint(func(rev Revision) ID { return ID(fmt.Sprintf("test-%d", rev)) }),
	)
}

// recvWithin reads one notification, failing instead of blocking forever when a
// regression makes a delivery disappear.
func recvWithin(t *testing.T, sub *Subscription) Notification {
	t.Helper()
	ctx, cancel := contextWithTimeout(5 * time.Second)
	defer cancel()
	notification, err := sub.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv() error = %v, want a notification", err)
	}
	return notification
}

func TestMetadataTypedValuesRoundTrip(t *testing.T) {
	stamp := NewTimestamp(time.Date(2026, time.March, 4, 5, 6, 7, 0, time.FixedZone("plus2", 2*60*60)))

	meta, err := Metadata{}.WithString("session_name", "reviewer")
	if err != nil {
		t.Fatalf("WithString: %v", err)
	}
	meta, err = meta.WithInt("exit_code", -1)
	if err != nil {
		t.Fatalf("WithInt: %v", err)
	}
	meta, err = meta.WithUint("output_bytes", 4096)
	if err != nil {
		t.Fatalf("WithUint: %v", err)
	}
	meta, err = meta.WithFloat("elapsed_seconds", 1.5)
	if err != nil {
		t.Fatalf("WithFloat: %v", err)
	}
	meta, err = meta.WithBool("orphan", true)
	if err != nil {
		t.Fatalf("WithBool: %v", err)
	}
	meta, err = meta.WithTimestamp("observed_at", stamp)
	if err != nil {
		t.Fatalf("WithTimestamp: %v", err)
	}
	meta, err = meta.WithStrings("providers", []string{"orca", "codex"})
	if err != nil {
		t.Fatalf("WithStrings: %v", err)
	}

	if got, ok := mustLookup(t, meta, "session_name").AsString(); !ok || got != "reviewer" {
		t.Fatalf("session_name = %q, %v, want reviewer", got, ok)
	}
	if got, ok := mustLookup(t, meta, "exit_code").AsInt64(); !ok || got != -1 {
		t.Fatalf("exit_code = %d, %v, want -1", got, ok)
	}
	if got, ok := mustLookup(t, meta, "output_bytes").AsUint64(); !ok || got != 4096 {
		t.Fatalf("output_bytes = %d, %v, want 4096", got, ok)
	}
	if got, ok := mustLookup(t, meta, "elapsed_seconds").AsFloat64(); !ok || got != 1.5 {
		t.Fatalf("elapsed_seconds = %v, %v, want 1.5", got, ok)
	}
	if got, ok := mustLookup(t, meta, "orphan").AsBool(); !ok || !got {
		t.Fatalf("orphan = %v, %v, want true", got, ok)
	}
	observed, ok := mustLookup(t, meta, "observed_at").AsTimestamp()
	if !ok {
		t.Fatal("observed_at is not a timestamp value")
	}
	if !observed.Time().Equal(stamp.Time()) {
		t.Fatalf("observed_at = %s, want %s", observed, stamp)
	}
	if observed.Time().Location() != time.UTC {
		t.Fatalf("observed_at location = %s, want UTC", observed.Time().Location())
	}
	providers, ok := mustLookup(t, meta, "providers").AsStrings()
	if !ok || len(providers) != 2 || providers[0] != "orca" || providers[1] != "codex" {
		t.Fatalf("providers = %v, %v, want [orca codex]", providers, ok)
	}
	if meta.Len() != 7 {
		t.Fatalf("Len() = %d, want 7", meta.Len())
	}
}

func TestMetadataAccessorsRejectWrongKind(t *testing.T) {
	meta, err := Metadata{}.WithString("session_name", "reviewer")
	if err != nil {
		t.Fatalf("WithString: %v", err)
	}
	if _, ok := mustLookup(t, meta, "session_name").AsInt64(); ok {
		t.Fatal("AsInt64 accepted a string value")
	}
	if _, ok := mustLookup(t, meta, "session_name").AsStrings(); ok {
		t.Fatal("AsStrings accepted a string value")
	}
	if _, ok := (Metadata{}).Lookup("absent"); ok {
		t.Fatal("Lookup reported a missing field as present")
	}
	if got := (Value{}).Kind(); got != KindInvalid {
		t.Fatalf("zero Value kind = %v, want invalid", got)
	}
}

func TestMetadataKindsAreTypedAndByteFree(t *testing.T) {
	values := []struct {
		name     string
		value    Value
		wantKind Kind
	}{
		{"string", StringValue("a"), KindString},
		{"int64", Int64Value(1), KindInt64},
		{"uint64", Uint64Value(1), KindUint64},
		{"float64", Float64Value(1), KindFloat64},
		{"bool", BoolValue(true), KindBool},
		{"timestamp", TimestampValue(NewTimestamp(time.Unix(0, 0))), KindTimestamp},
		{"strings", StringsValue([]string{"a"}), KindStrings},
	}
	for _, tt := range values {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.Kind(); got != tt.wantKind {
				t.Fatalf("Kind(%s) = %v, want %v", tt.name, got, tt.wantKind)
			}
			if got := tt.wantKind.String(); got == "" || got == "unknown" || got == "invalid" {
				t.Fatalf("Kind(%s).String() = %q", tt.name, got)
			}
		})
	}
	if (Value{}).Format() != "<invalid>" {
		t.Fatalf("zero Value Format() = %q, want <invalid>", (Value{}).Format())
	}
}

func TestMetadataIsImmutable(t *testing.T) {
	base, err := Metadata{}.WithString("session_name", "reviewer")
	if err != nil {
		t.Fatalf("WithString: %v", err)
	}
	derived, err := base.WithString("workspace_path", "/srv/work")
	if err != nil {
		t.Fatalf("second WithString: %v", err)
	}
	if base.Len() != 1 {
		t.Fatalf("base Len() = %d, want 1: With must not mutate the receiver", base.Len())
	}
	if derived.Len() != 2 {
		t.Fatalf("derived Len() = %d, want 2", derived.Len())
	}
	if _, ok := base.Lookup("workspace_path"); ok {
		t.Fatal("base metadata gained a field from the derived copy")
	}

	items := []string{"orca"}
	listMeta, err := Metadata{}.WithStrings("providers", items)
	if err != nil {
		t.Fatalf("WithStrings: %v", err)
	}
	items[0] = "mutated"
	stored, _ := listMeta.Lookup("providers")
	if elements, _ := stored.AsStrings(); elements[0] != "orca" {
		t.Fatalf("providers = %v, want the value to be copied at construction", elements)
	}
}

func TestMetadataFieldNamePolicy(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   Value
		wantErr error
	}{
		{name: "session scoped text", key: "session_name", value: StringValue("reviewer")},
		{name: "lifecycle", key: "lifecycle", value: StringValue("running")},
		{name: "provider", key: "provider.name", value: StringValue("orca")},
		{name: "exit reason", key: "exit_reason", value: StringValue("killed")},
		{name: "numeric output size stays allowed", key: "output_bytes", value: Uint64Value(1024)},
		{name: "byte count of a prompt stays allowed", key: "prompt_bytes", value: Uint64Value(1024)},

		{name: "stdout text", key: "stdout_tail", value: StringValue("build ok"), wantErr: ErrForbiddenMetadataKey},
		{name: "terminal text", key: "terminal_title", value: StringValue("bash"), wantErr: ErrForbiddenMetadataKey},
		{name: "scrollback", key: "scrollback", value: StringValue("..."), wantErr: ErrForbiddenMetadataKey},
		{name: "raw output", key: "raw_output", value: StringValue("..."), wantErr: ErrForbiddenMetadataKey},
		{name: "prompt text", key: "prompt_text", value: StringValue("build"), wantErr: ErrForbiddenMetadataKey},
		{name: "string slice of output", key: "output_lines", value: StringsValue([]string{"a"}), wantErr: ErrForbiddenMetadataKey},
		{name: "folded case", key: "StdOut", value: StringValue("..."), wantErr: ErrForbiddenMetadataKey},
		{name: "command line", key: "cmdline", value: StringValue("..."), wantErr: ErrForbiddenMetadataKey},

		{name: "api key", key: "api_key", value: StringValue("sk-..."), wantErr: ErrForbiddenMetadataKey},
		{name: "folded api key", key: "API.Key", value: StringValue("sk-..."), wantErr: ErrForbiddenMetadataKey},
		{name: "env var", key: "env", value: StringValue("PATH"), wantErr: ErrForbiddenMetadataKey},
		{name: "environment", key: "environment_mode", value: StringValue("ci"), wantErr: ErrForbiddenMetadataKey},
		{name: "secret in any kind", key: "exit_secret", value: Int64Value(7), wantErr: ErrForbiddenMetadataKey},
		{name: "token in any kind", key: "token_count", value: Int64Value(7), wantErr: ErrForbiddenMetadataKey},
		{name: "argv", key: "argv", value: StringsValue([]string{"orca"}), wantErr: ErrForbiddenMetadataKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, err := Metadata{}.With(tt.key, tt.value)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("With(%q) error = %v, want %v", tt.key, err, tt.wantErr)
			}
			if tt.wantErr != nil && !meta.IsEmpty() {
				t.Fatalf("With(%q) returned %s on rejection, want the receiver unchanged", tt.key, meta.Format())
			}
		})
	}
}

func TestMetadataValuePolicyRejectsTerminalBytes(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   Value
		wantErr error
	}{
		{name: "escape sequence", key: "session_name", value: StringValue("ready\x1b[2J"), wantErr: ErrForbiddenMetadataValue},
		{name: "carriage return", key: "session_name", value: StringValue("a\rb"), wantErr: ErrForbiddenMetadataValue},
		{name: "nul byte", key: "session_name", value: StringValue("a\x00b"), wantErr: ErrForbiddenMetadataValue},
		{name: "delete byte", key: "session_name", value: StringValue("a\x7fb"), wantErr: ErrForbiddenMetadataValue},
		{name: "invalid utf8", key: "session_name", value: StringValue("a\xffb"), wantErr: ErrForbiddenMetadataValue},
		{name: "escape in a list element", key: "providers", value: StringsValue([]string{"orca", "a\x1bb"}), wantErr: ErrForbiddenMetadataValue},
		{name: "oversize string", key: "session_name", value: StringValue(strings.Repeat("a", MaxMetadataValueLen+1)), wantErr: ErrMetadataValueLimit},
		{name: "oversize list", key: "providers", value: StringsValue(make([]string, MaxMetadataStringsLen+1)), wantErr: ErrMetadataValueLimit},
		{name: "empty name", key: "", value: StringValue("a"), wantErr: ErrInvalidMetadataKey},
		{name: "zero value", key: "session_name", value: Value{}, wantErr: ErrInvalidMetadataValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := (Metadata{}).With(tt.key, tt.value); !errors.Is(err, tt.wantErr) {
				t.Fatalf("With(%q) error = %v, want %v", tt.key, err, tt.wantErr)
			}
		})
	}

	atLimit, err := Metadata{}.WithString("session_name", strings.Repeat("a", MaxMetadataValueLen))
	if err != nil {
		t.Fatalf("WithString at the length limit: %v", err)
	}
	if got := len(mustLookup(t, atLimit, "session_name").mustString(t)); got != MaxMetadataValueLen {
		t.Fatalf("stored length = %d, want %d", got, MaxMetadataValueLen)
	}
}

func TestMetadataFieldLimit(t *testing.T) {
	meta := Metadata{}
	for i := range MaxMetadataFields {
		var err error
		meta, err = meta.WithString(fmt.Sprintf("field_%02d", i), "v")
		if err != nil {
			t.Fatalf("field %d: %v", i, err)
		}
	}
	if _, err := meta.WithString("one_too_many", "v"); !errors.Is(err, ErrMetadataFieldLimit) {
		t.Fatalf("WithString beyond the limit error = %v, want %v", err, ErrMetadataFieldLimit)
	}
	replaced, err := meta.WithString("field_00", "replaced")
	if err != nil {
		t.Fatalf("replacing an existing field at the limit: %v", err)
	}
	if got := mustLookup(t, replaced, "field_00").mustString(t); got != "replaced" {
		t.Fatalf("field_00 = %q, want replaced", got)
	}
}

func TestMetadataKeysAndFormatAreDeterministic(t *testing.T) {
	build := func() Metadata {
		meta := Metadata{}
		for _, field := range []struct {
			key   string
			value Value
		}{
			{"workspace_path", StringValue("/srv/work")},
			{"session_name", StringValue("reviewer")},
			{"orphan", BoolValue(false)},
			{"providers", StringsValue([]string{"orca", "codex"})},
			{"elapsed_seconds", Float64Value(1.5)},
		} {
			var err error
			if meta, err = meta.With(field.key, field.value); err != nil {
				t.Fatalf("With(%q): %v", field.key, err)
			}
		}
		return meta
	}
	first, second := build(), build()

	wantKeys := []string{"elapsed_seconds", "orphan", "providers", "session_name", "workspace_path"}
	if got := first.Keys(); !equalStrings(got, wantKeys) {
		t.Fatalf("Keys() = %v, want %v", got, wantKeys)
	}
	const wantFormat = `elapsed_seconds=1.5 orphan=false providers=["orca" "codex"] session_name="reviewer" workspace_path="/srv/work"`
	if got := first.Format(); got != wantFormat {
		t.Fatalf("Format() = %q, want %q", got, wantFormat)
	}
	if first.Format() != second.Format() {
		t.Fatalf("Format() is not stable: %q vs %q", first.Format(), second.Format())
	}
	if !first.Equal(second) {
		t.Fatal("Equal() reported identical metadata as different")
	}
	other, err := second.WithString("session_name", "other")
	if err != nil {
		t.Fatalf("WithString: %v", err)
	}
	if first.Equal(other) {
		t.Fatal("Equal() reported different metadata as equal")
	}
}

func TestValueEqualAndFormat(t *testing.T) {
	stamp := NewTimestamp(time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC))
	tests := []struct {
		name   string
		value  Value
		other  Value
		equal  bool
		format string
	}{
		{name: "string", value: StringValue("a"), other: StringValue("a"), equal: true, format: `"a"`},
		{name: "int", value: Int64Value(-3), other: Int64Value(-3), equal: true, format: "-3"},
		{name: "uint", value: Uint64Value(7), other: Uint64Value(8), equal: false, format: "7"},
		{name: "float", value: Float64Value(0.5), other: Float64Value(0.25), equal: false, format: "0.5"},
		{name: "bool", value: BoolValue(true), other: BoolValue(true), equal: true, format: "true"},
		{name: "timestamp", value: TimestampValue(stamp), other: TimestampValue(stamp), equal: true, format: `"` + stamp.String() + `"`},
		{name: "strings", value: StringsValue([]string{"a", "b"}), other: StringsValue([]string{"a", "b"}), equal: true, format: `["a" "b"]`},
		{name: "different kind", value: Int64Value(1), other: Uint64Value(1), equal: false, format: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.Equal(tt.other); got != tt.equal {
				t.Fatalf("Equal() = %v, want %v", got, tt.equal)
			}
			if got := tt.value.Format(); got != tt.format {
				t.Fatalf("Format() = %q, want %q", got, tt.format)
			}
		})
	}
}

func TestKindNamesAndAccessorsRejectForeignKinds(t *testing.T) {
	names := map[Kind]string{
		KindInvalid:   "invalid",
		KindString:    "string",
		KindInt64:     "int64",
		KindUint64:    "uint64",
		KindFloat64:   "float64",
		KindBool:      "bool",
		KindTimestamp: "timestamp",
		KindStrings:   "strings",
		Kind(200):     "unknown",
	}
	for kind, want := range names {
		if got := kind.String(); got != want {
			t.Fatalf("Kind(%d).String() = %q, want %q", kind, got, want)
		}
	}

	number := Int64Value(1)
	if got, ok := number.AsInt64(); !ok || got != 1 {
		t.Fatalf("AsInt64() = %d, %v, want 1, true", got, ok)
	}
	if _, ok := number.AsString(); ok {
		t.Fatal("AsString accepted an int64 value")
	}
	if _, ok := number.AsUint64(); ok {
		t.Fatal("AsUint64 accepted an int64 value")
	}
	if _, ok := number.AsFloat64(); ok {
		t.Fatal("AsFloat64 accepted an int64 value")
	}
	if _, ok := number.AsBool(); ok {
		t.Fatal("AsBool accepted an int64 value")
	}
	if _, ok := number.AsTimestamp(); ok {
		t.Fatal("AsTimestamp accepted an int64 value")
	}
	if _, ok := number.AsStrings(); ok {
		t.Fatal("AsStrings accepted an int64 value")
	}
	zero := Value{}
	if !zero.Equal(Value{}) {
		t.Fatal("two zero values are not equal")
	}

	meta, err := Metadata{}.WithInt("exit_code", 1)
	if err != nil {
		t.Fatalf("WithInt: %v", err)
	}
	other, err := meta.WithInt("exit_reason", 1)
	if err != nil {
		t.Fatalf("WithInt: %v", err)
	}
	if meta.Equal(other) {
		t.Fatal("Equal() ignored a differing field name")
	}
}

func mustLookup(t *testing.T, meta Metadata, key string) Value {
	t.Helper()
	value, ok := meta.Lookup(key)
	if !ok {
		t.Fatalf("metadata field %q is missing; fields = %v", key, meta.Keys())
	}
	return value
}

func (v Value) mustString(t *testing.T) string {
	t.Helper()
	s, ok := v.AsString()
	if !ok {
		t.Fatalf("value %v is not a string", v.Kind())
	}
	return s
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
