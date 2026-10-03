package agent

import (
	"fmt"
	"strings"
)

// Capability is one launch-adjacent operation an Agent definition may support.
//
// The split mirrors the plan's core-versus-provider decision: ASD owns the
// lifecycle of everything it launches, while list/log/resume/kill against a
// vendor's own session model is a native provider capability that stays off
// until an adapter plus evidence exists (PRODUCT.md §8, plan README decision 4).
type Capability string

const (
	// CapabilityLaunch resolves a command for a workspace. Core: every agent ASD
	// can run at all has it.
	CapabilityLaunch Capability = "launch"
	// CapabilityInteractive accepts an interactive PTY session. Core: ASD drives
	// the terminal itself, so this is not a vendor feature.
	CapabilityInteractive Capability = "interactive"

	// CapabilitySessionList lists the vendor's own sessions. Native.
	CapabilitySessionList Capability = "session_list"
	// CapabilitySessionLogs reads the vendor's own session output. Native.
	CapabilitySessionLogs Capability = "session_logs"
	// CapabilityResume continues a vendor conversation. Native and off by
	// default: restart re-runs the resolved command, it never resumes a
	// conversation (plan README decision 4, transition T11/T12).
	CapabilityResume Capability = "resume"
	// CapabilityKill terminates through the vendor API. Native: the core kill
	// path always signals the verified owned process group.
	CapabilityKill Capability = "kill"
)

// CapabilityClass separates ASD core lifecycle capabilities from native vendor
// capabilities.
type CapabilityClass string

const (
	// ClassCore is provided by the ASD runtime for every launched session.
	ClassCore CapabilityClass = "core"
	// ClassNative is provided by the vendor agent itself.
	ClassNative CapabilityClass = "native"
)

// AllCapabilities returns every capability in a stable order.
func AllCapabilities() []Capability {
	return []Capability{
		CapabilityLaunch,
		CapabilityInteractive,
		CapabilitySessionList,
		CapabilitySessionLogs,
		CapabilityResume,
		CapabilityKill,
	}
}

// String returns the wire name of the capability.
func (c Capability) String() string { return string(c) }

// Class reports which side of the core/provider boundary the capability sits
// on, or the empty class when the capability is unknown.
func (c Capability) Class() CapabilityClass {
	switch c {
	case CapabilityLaunch, CapabilityInteractive:
		return ClassCore
	case CapabilitySessionList, CapabilitySessionLogs, CapabilityResume, CapabilityKill:
		return ClassNative
	default:
		return ""
	}
}

// Valid reports whether the capability is one of the defined ones.
func (c Capability) Valid() bool { return c.Class() != "" }

// IsNative reports whether the capability requires a vendor adapter.
func (c Capability) IsNative() bool { return c.Class() == ClassNative }

type capabilityBit uint16

const (
	bitLaunch capabilityBit = 1 << iota
	bitInteractive
	bitSessionList
	bitSessionLogs
	bitResume
	bitKill
)

var capabilityBits = map[Capability]capabilityBit{
	CapabilityLaunch:      bitLaunch,
	CapabilityInteractive: bitInteractive,
	CapabilitySessionList: bitSessionList,
	CapabilitySessionLogs: bitSessionLogs,
	CapabilityResume:      bitResume,
	CapabilityKill:        bitKill,
}

// Capabilities is an immutable set of capabilities.
//
// It is a bitset rather than a map or slice so copying a set never shares
// mutable state: every mutation returns a new value, and Provider can hand the
// set to callers without defensive cloning.
type Capabilities uint16

// CapabilitiesOf builds a capability set, rejecting unknown capabilities so a
// typo in a provider adapter fails loudly instead of disabling a feature.
func CapabilitiesOf(caps ...Capability) (Capabilities, error) {
	var set Capabilities
	for _, cap := range caps {
		bit, ok := capabilityBits[cap]
		if !ok {
			return 0, &UnsupportedError{AgentID: "", Capability: cap}
		}
		set |= Capabilities(bit)
	}
	return set, nil
}

// MustCapabilitiesOf is CapabilitiesOf for compile-time-known capability lists.
// It panics on an unknown capability, which can only be a programming error.
func MustCapabilitiesOf(caps ...Capability) Capabilities {
	set, err := CapabilitiesOf(caps...)
	if err != nil {
		panic(err)
	}
	return set
}

// Has reports whether the set contains the capability. An unknown capability is
// never reported as supported.
func (c Capabilities) Has(cap Capability) bool {
	bit, ok := capabilityBits[cap]
	return ok && c&Capabilities(bit) != 0
}

// With returns a copy of the set with the given capabilities added. Unknown
// capabilities are ignored; use CapabilitiesOf to detect them.
func (c Capabilities) With(caps ...Capability) Capabilities {
	for _, cap := range caps {
		if bit, ok := capabilityBits[cap]; ok {
			c |= Capabilities(bit)
		}
	}
	return c
}

// Without returns a copy of the set with the given capabilities removed.
func (c Capabilities) Without(caps ...Capability) Capabilities {
	for _, cap := range caps {
		if bit, ok := capabilityBits[cap]; ok {
			c &^= Capabilities(bit)
		}
	}
	return c
}

// Supports returns nil when the capability is present and a typed
// *UnsupportedError when it is not, so an unsupported native operation fails
// with the UNSUPPORTED code instead of a generic error.
func (c Capabilities) Supports(cap Capability) error {
	if c.Has(cap) {
		return nil
	}
	return &UnsupportedError{Capability: cap}
}

// List returns the capabilities in a stable order.
func (c Capabilities) List() []Capability {
	var out []Capability
	for _, cap := range AllCapabilities() {
		if c.Has(cap) {
			out = append(out, cap)
		}
	}
	return out
}

// Core returns only the ASD-owned lifecycle capabilities present in the set.
func (c Capabilities) Core() Capabilities {
	return c.Without(CapabilitySessionList, CapabilitySessionLogs, CapabilityResume, CapabilityKill)
}

// Native returns only the vendor capabilities present in the set.
func (c Capabilities) Native() Capabilities {
	return c.Without(CapabilityLaunch, CapabilityInteractive)
}

// String renders the set in a stable order, e.g. "interactive, launch".
func (c Capabilities) String() string {
	list := c.List()
	names := make([]string, 0, len(list))
	for _, cap := range list {
		names = append(names, string(cap))
	}
	return strings.Join(names, ", ")
}

// UnsupportedError reports a capability an agent definition does not provide.
// The session layer maps it to the UNSUPPORTED code.
type UnsupportedError struct {
	// AgentID is filled in when the provider that answered knows its own id.
	AgentID ID
	// Capability is the capability that was requested.
	Capability Capability
}

func (e *UnsupportedError) Error() string {
	class := e.Capability.Class()
	if class == "" {
		return fmt.Sprintf("agent %s does not support unknown capability %q", e.describeAgent(), string(e.Capability))
	}
	return fmt.Sprintf("agent %s does not support %s capability %q", e.describeAgent(), class, string(e.Capability))
}

func (e *UnsupportedError) describeAgent() string {
	if e.AgentID == "" {
		return "(unidentified)"
	}
	return string(e.AgentID)
}
