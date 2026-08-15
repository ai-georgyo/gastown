// Package channelevents provides file-based event emission for named channels.
//
// Channel events are JSON files written to a channel directory and consumed by
// await-event subscribers (e.g., the refinery watching for MERGE_READY events).
// This is distinct from the activity feed events in the events package
// (~/gt/.events.jsonl).
//
// Channels are either town-level or rig-scoped:
//
//	town-level:  ~/gt/events/<channel>/*.event            (mayor, deacon, ...)
//	rig-scoped:  ~/gt/events/rigs/<rig>/<channel>/*.event  (refinery, witness)
//
// Rig scoping matters because a channel is single-consumer: every event file in
// a directory is delivered to — and, with --cleanup, deleted by — whichever
// subscriber reads it first. A town-global "refinery" directory therefore made
// every rig's refinery wake for every other rig's MQ_SUBMIT, and let the wrong
// refinery consume an event addressed to another rig (gt-em1).
package channelevents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// ValidChannelName restricts channel names to safe characters (no path traversal).
var ValidChannelName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidRigName restricts rig names to safe characters (no path traversal).
var ValidRigName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// rigScopedChannels lists the channels whose consumer is a per-rig agent.
// Events on these channels live under <town>/events/rigs/<rig>/<channel>/ so
// only the owning rig's agent is woken by them and can consume them.
//
// Channels not listed here are town-level: they have exactly one consumer for
// the whole town (the mayor, the deacon), so a shared directory is correct.
var rigScopedChannels = map[string]bool{
	"refinery": true,
	"witness":  true,
}

// IsRigScoped reports whether events on this channel belong to a single rig.
func IsRigScoped(channel string) bool {
	return rigScopedChannels[channel]
}

// emitSeq is an atomic counter to ensure unique event filenames even when
// time.Now().UnixNano() has low resolution.
var emitSeq atomic.Uint64

// rigsSubdir is the directory under events/ that holds the per-rig channel
// trees. It is reserved: a town-level channel of the same name would sit on
// top of it.
const rigsSubdir = "rigs"

// Dir returns the directory holding events for a channel. An empty rig selects
// the town-level directory; a non-empty rig selects that rig's private one.
func Dir(townRoot, rig, channel string) (string, error) {
	if !ValidChannelName.MatchString(channel) {
		return "", fmt.Errorf("invalid channel name %q: must match [a-zA-Z0-9_-]", channel)
	}
	if channel == rigsSubdir {
		return "", fmt.Errorf("channel name %q is reserved for rig-scoped channel directories", channel)
	}
	if rig == "" {
		return filepath.Join(townRoot, "events", channel), nil
	}
	if !ValidRigName.MatchString(rig) {
		return "", fmt.Errorf("invalid rig name %q: must match [a-zA-Z0-9_-]", rig)
	}
	return filepath.Join(townRoot, "events", rigsSubdir, rig, channel), nil
}

// EmitToTown creates an event file on a town-level channel.
// Used by internal callers that already know the town root.
func EmitToTown(townRoot, channel, eventType string, payloadPairs []string) (string, error) {
	return EmitToRig(townRoot, "", channel, eventType, payloadPairs)
}

// EmitToRig creates an event file on a rig's private channel directory.
// An empty rig is equivalent to EmitToTown.
func EmitToRig(townRoot, rig, channel, eventType string, payloadPairs []string) (string, error) {
	eventDir, err := Dir(townRoot, rig, channel)
	if err != nil {
		return "", err
	}
	// Checked before MkdirAll: creating the channel directory is itself a
	// write to the town's event bus. See sandbox.go.
	if err := checkEmitAllowed(eventDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(eventDir, 0755); err != nil {
		return "", fmt.Errorf("creating event directory: %w", err)
	}
	return emitToDir(eventDir, rig, channel, eventType, payloadPairs)
}

// emitToDir writes an event file to the given directory.
func emitToDir(eventDir, rig, channel, eventType string, payloadPairs []string) (string, error) {
	if !ValidChannelName.MatchString(channel) {
		return "", fmt.Errorf("invalid channel name %q: must match [a-zA-Z0-9_-]", channel)
	}
	// Re-checked at the point of the write so the sandbox holds for any future
	// caller that reaches emitToDir without going through EmitToTown.
	if err := checkEmitAllowed(eventDir); err != nil {
		return "", err
	}

	payload := make(map[string]string)
	for _, pair := range payloadPairs {
		key, val, found := strings.Cut(pair, "=")
		if found {
			payload[key] = val
		}
	}

	now := time.Now()
	event := map[string]interface{}{
		"type":      eventType,
		"channel":   channel,
		"timestamp": now.Format(time.RFC3339),
		"payload":   payload,
	}
	// Address the event to its rig so a reader can tell at a glance who it
	// was for, even if the file is later moved or inspected by hand.
	if rig != "" {
		event["rig"] = rig
	}

	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling event: %w", err)
	}

	seq := emitSeq.Add(1)
	eventFile := filepath.Join(eventDir, fmt.Sprintf("%d-%d-%d.event", now.UnixNano(), seq, os.Getpid()))
	if err := os.WriteFile(eventFile, data, 0644); err != nil {
		return "", fmt.Errorf("writing event file: %w", err)
	}

	return eventFile, nil
}
