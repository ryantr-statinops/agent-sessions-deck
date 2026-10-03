package events

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Metadata bounds. Events are in-memory state-change notifications, not a
// transport for payloads, so both the field count and every value length are
// capped.
const (
	// MaxMetadataFields is the number of fields one event's metadata may hold.
	MaxMetadataFields = 32
	// MaxMetadataValueLen is the maximum length of one string, or of one
	// element of a string-slice value.
	MaxMetadataValueLen = 4096
	// MaxMetadataStringsLen is the maximum number of elements in a
	// string-slice value.
	MaxMetadataStringsLen = 32
)

// Kind enumerates the typed metadata values an event may carry. There is no
// byte-slug kind: an event cannot hold raw terminal bytes.
type Kind uint8

// The metadata value kinds.
const (
	KindInvalid Kind = iota
	KindString
	KindInt64
	KindUint64
	KindFloat64
	KindBool
	KindTimestamp
	KindStrings
)

// String returns the kind name used in diagnostics.
func (k Kind) String() string {
	switch k {
	case KindString:
		return "string"
	case KindInt64:
		return "int64"
	case KindUint64:
		return "uint64"
	case KindFloat64:
		return "float64"
	case KindBool:
		return "bool"
	case KindTimestamp:
		return "timestamp"
	case KindStrings:
		return "strings"
	case KindInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// Value is one typed metadata value. Build values with the constructor
// functions below; the zero Value is invalid and never stored.
type Value struct {
	kind Kind
	str  string
	i    int64
	u    uint64
	f    float64
	b    bool
	ts   Timestamp
	list []string
}

// StringValue returns a text value. The text must be printable UTF-8; the
// [Metadata.With] call that stores it enforces that.
func StringValue(s string) Value { return Value{kind: KindString, str: s} }

// Int64Value returns a signed integer value.
func Int64Value(i int64) Value { return Value{kind: KindInt64, i: i} }

// Uint64Value returns an unsigned integer value.
func Uint64Value(u uint64) Value { return Value{kind: KindUint64, u: u} }

// Float64Value returns a floating point value.
func Float64Value(f float64) Value { return Value{kind: KindFloat64, f: f} }

// BoolValue returns a boolean value.
func BoolValue(b bool) Value { return Value{kind: KindBool, b: b} }

// TimestampValue returns a UTC instant value.
func TimestampValue(t Timestamp) Value { return Value{kind: KindTimestamp, ts: t} }

// StringsValue returns a string-slice value. The slice is copied, so the caller
// cannot change the value afterwards.
func StringsValue(items []string) Value {
	copied := make([]string, len(items))
	copy(copied, items)
	return Value{kind: KindStrings, list: copied}
}

// Kind returns the value kind, [KindInvalid] for the zero Value.
func (v Value) Kind() Kind { return v.kind }

// AsString returns the text of a [KindString] value.
func (v Value) AsString() (string, bool) {
	if v.kind != KindString {
		return "", false
	}
	return v.str, true
}

// AsInt64 returns the number of a [KindInt64] value.
func (v Value) AsInt64() (int64, bool) {
	if v.kind != KindInt64 {
		return 0, false
	}
	return v.i, true
}

// AsUint64 returns the number of a [KindUint64] value.
func (v Value) AsUint64() (uint64, bool) {
	if v.kind != KindUint64 {
		return 0, false
	}
	return v.u, true
}

// AsFloat64 returns the number of a [KindFloat64] value.
func (v Value) AsFloat64() (float64, bool) {
	if v.kind != KindFloat64 {
		return 0, false
	}
	return v.f, true
}

// AsBool returns the flag of a [KindBool] value.
func (v Value) AsBool() (bool, bool) {
	if v.kind != KindBool {
		return false, false
	}
	return v.b, true
}

// AsTimestamp returns the instant of a [KindTimestamp] value.
func (v Value) AsTimestamp() (Timestamp, bool) {
	if v.kind != KindTimestamp {
		return Timestamp{}, false
	}
	return v.ts, true
}

// AsStrings returns a copy of the elements of a [KindStrings] value.
func (v Value) AsStrings() ([]string, bool) {
	if v.kind != KindStrings {
		return nil, false
	}
	copied := make([]string, len(v.list))
	copy(copied, v.list)
	return copied, true
}

// Equal reports whether two values have the same kind and payload.
func (v Value) Equal(other Value) bool {
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case KindString:
		return v.str == other.str
	case KindInt64:
		return v.i == other.i
	case KindUint64:
		return v.u == other.u
	case KindFloat64:
		return v.f == other.f
	case KindBool:
		return v.b == other.b
	case KindTimestamp:
		return v.ts == other.ts
	case KindStrings:
		return slices.Equal(v.list, other.list)
	default:
		return true
	}
}

// Format renders the value deterministically for logs and test failures.
func (v Value) Format() string {
	switch v.kind {
	case KindString:
		return strconv.Quote(v.str)
	case KindInt64:
		return strconv.FormatInt(v.i, 10)
	case KindUint64:
		return strconv.FormatUint(v.u, 10)
	case KindFloat64:
		return strconv.FormatFloat(v.f, 'g', -1, 64)
	case KindBool:
		return strconv.FormatBool(v.b)
	case KindTimestamp:
		return strconv.Quote(v.ts.String())
	case KindStrings:
		parts := make([]string, len(v.list))
		for i, item := range v.list {
			parts[i] = strconv.Quote(item)
		}
		return "[" + strings.Join(parts, " ") + "]"
	default:
		return "<invalid>"
	}
}

// validateFieldName enforces the metadata naming policy.
//
// Names that smell like terminal output are refused for text values only, since
// a number cannot carry bytes. Names that smell like credentials are refused
// for every kind, because a numeric secret is still a secret.
func validateFieldName(key string, kind Kind) error {
	normalized := normalizeFieldName(key)
	words := strings.Split(normalized, "_")
	if matchesAny(words, secretFieldNames) || containsAny(normalized, secretFieldSubstrings) {
		return fmt.Errorf("%w: %q looks like environment or secret material", ErrForbiddenMetadataKey, key)
	}
	if carriesText(kind) && (matchesAny(words, byteFieldNames) || containsAny(normalized, byteFieldSubstrings)) {
		return fmt.Errorf("%w: %q looks like terminal output", ErrForbiddenMetadataKey, key)
	}
	return nil
}

// normalizeFieldName lowercases the name and folds separators so that
// "API.Key", "api-key" and "api_key" are one field name for policy purposes.
func normalizeFieldName(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range key {
		switch r {
		case '.', '-', ' ', '/':
			b.WriteByte('_')
		default:
			if r >= 'A' && r <= 'Z' {
				r += 'a' - 'A'
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func matchesAny(words []string, denied []string) bool {
	for _, word := range words {
		if slices.Contains(denied, word) {
			return true
		}
	}
	return false
}

func containsAny(normalized string, denied []string) bool {
	for _, fragment := range denied {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func carriesText(kind Kind) bool {
	return kind == KindString || kind == KindStrings
}

// byteFieldNames and byteFieldSubstrings name terminal or transcript content.
// They are refused for text values; a producer that wants to report a size uses
// an integer field such as "exit_output_bytes" (numeric, therefore allowed).
var (
	byteFieldNames = []string{
		"ansi", "argvs", "bytes", "cells", "console", "cursor", "esc",
		"grid", "output", "outputs", "prompt", "pty", "raw", "screen",
		"scrollback", "stdin", "stdout", "stderr", "term", "terminal",
		"transcript", "tty",
	}
	byteFieldSubstrings = []string{
		"ansi_escape", "argv", "byte", "cmdline", "command_line",
		"commandline", "escape_seq", "escape_sequence", "scrollback",
		"terminal", "transcript", "tty",
	}
)

// secretFieldNames and secretFieldSubstrings name environment or credential
// material and are refused for every value kind.
var (
	secretFieldNames = []string{
		"auth", "authorization", "bearer", "cookie", "cookies", "credential",
		"credentials", "env", "environ", "environment", "passwd",
		"passphrase", "password", "secret", "secrets", "token", "tokens",
	}
	secretFieldSubstrings = []string{
		"api_key", "apikey", "credential", "passphrase", "passwd",
		"password", "private_key", "secret", "session_token", "token",
	}
)

// validateValueText enforces the "no raw terminal bytes" rule on text values:
// valid UTF-8 without control bytes, so no escape sequence can ride along.
func validateValueText(field, value string) error {
	if len(value) > MaxMetadataValueLen {
		return fmt.Errorf("%w: %q is %d bytes, limit is %d", ErrMetadataValueLimit, field, len(value), MaxMetadataValueLen)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %q is not valid UTF-8", ErrForbiddenMetadataValue, field)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %q contains a control byte", ErrForbiddenMetadataValue, field)
		}
	}
	return nil
}

// Metadata is the immutable typed field set of an event. The zero value is an
// empty, valid metadata set, and every mutating helper returns a new value, so
// an event handed to a subscriber can never be changed underneath it.
type Metadata struct {
	fields map[string]Value
}

// Len returns the number of fields.
func (m Metadata) Len() int { return len(m.fields) }

// IsEmpty reports whether the metadata has no fields.
func (m Metadata) IsEmpty() bool { return len(m.fields) == 0 }

// Keys returns the field names in sorted order.
func (m Metadata) Keys() []string {
	keys := make([]string, 0, len(m.fields))
	for key := range m.fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// Lookup returns the value stored under key.
func (m Metadata) Lookup(key string) (Value, bool) {
	v, ok := m.fields[key]
	return v, ok
}

// With returns a copy of m with key set to v. The field name and value are
// validated against the metadata policy; on error the receiver is returned
// unchanged together with the error.
func (m Metadata) With(key string, v Value) (Metadata, error) {
	if key == "" {
		return m, ErrInvalidMetadataKey
	}
	if v.kind == KindInvalid {
		return m, fmt.Errorf("%w: field %q", ErrInvalidMetadataValue, key)
	}
	if err := validateFieldName(key, v.kind); err != nil {
		return m, err
	}
	switch v.kind {
	case KindString:
		if err := validateValueText(key, v.str); err != nil {
			return m, err
		}
	case KindStrings:
		if len(v.list) > MaxMetadataStringsLen {
			return m, fmt.Errorf("%w: %q has %d elements, limit is %d", ErrMetadataValueLimit, key, len(v.list), MaxMetadataStringsLen)
		}
		for _, item := range v.list {
			if err := validateValueText(key, item); err != nil {
				return m, err
			}
		}
	}
	updated := m.clone()
	if updated.fields == nil {
		updated.fields = make(map[string]Value, 1)
	}
	if _, replacing := updated.fields[key]; !replacing && len(updated.fields) >= MaxMetadataFields {
		return m, fmt.Errorf("%w: %q would be field %d, limit is %d", ErrMetadataFieldLimit, key, len(updated.fields)+1, MaxMetadataFields)
	}
	updated.fields[key] = v
	return updated, nil
}

// WithString returns a copy of m with a text field set.
func (m Metadata) WithString(key, value string) (Metadata, error) {
	return m.With(key, StringValue(value))
}

// WithInt returns a copy of m with a signed integer field set.
func (m Metadata) WithInt(key string, value int64) (Metadata, error) {
	return m.With(key, Int64Value(value))
}

// WithUint returns a copy of m with an unsigned integer field set.
func (m Metadata) WithUint(key string, value uint64) (Metadata, error) {
	return m.With(key, Uint64Value(value))
}

// WithFloat returns a copy of m with a floating point field set.
func (m Metadata) WithFloat(key string, value float64) (Metadata, error) {
	return m.With(key, Float64Value(value))
}

// WithBool returns a copy of m with a boolean field set.
func (m Metadata) WithBool(key string, value bool) (Metadata, error) {
	return m.With(key, BoolValue(value))
}

// WithTimestamp returns a copy of m with a UTC instant field set.
func (m Metadata) WithTimestamp(key string, value Timestamp) (Metadata, error) {
	return m.With(key, TimestampValue(value))
}

// WithStrings returns a copy of m with a string-slice field set.
func (m Metadata) WithStrings(key string, value []string) (Metadata, error) {
	return m.With(key, StringsValue(value))
}

// Equal reports whether two metadata sets hold the same fields.
func (m Metadata) Equal(other Metadata) bool {
	if len(m.fields) != len(other.fields) {
		return false
	}
	for key, v := range m.fields {
		ov, ok := other.fields[key]
		if !ok || !v.Equal(ov) {
			return false
		}
	}
	return true
}

// Format renders the fields deterministically, sorted by name, for logs and
// test failures.
func (m Metadata) Format() string {
	keys := m.Keys()
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+m.fields[key].Format())
	}
	return strings.Join(parts, " ")
}

func (m Metadata) clone() Metadata {
	if m.fields == nil {
		return Metadata{}
	}
	fields := make(map[string]Value, len(m.fields))
	for key, v := range m.fields {
		fields[key] = v
	}
	return Metadata{fields: fields}
}
