# M1 Implementation Empirical Challenge Report

**Target Components**: `utils/pairing_code.go`, `utils/outbox.go`, `utils/worker_daemon.go`  
**Challenger**: Challenger 1 (Milestone 1)  
**Date**: 2026-07-28  
**Verification Environment**: Go 1.22+ Windows / Race Detector (`-race`)

---

## Executive Summary

An empirical adversarial review and stress testing of `utils/pairing_code.go`, `utils/outbox.go`, and `utils/worker_daemon.go` was conducted. The testing suite evaluated high concurrency, outbox buffer overflows, mock WebSocket drop/reconnect stress, race conditions, memory safety, and parameter edge cases.

### Summary of Findings
| Finding ID | Component | Severity | Description | Status |
|------------|-----------|----------|-------------|--------|
| **FINDING-M1-01** | `pairing_code.go` | **CRITICAL** | Data Race & Interior Pointer Leak in `LoadCredentials()` | Confirmed empirically |
| **FINDING-M1-02** | `outbox.go` | **HIGH** | `Attempts` Counter Increment Lost on Delivery Failure | Confirmed empirically |
| **FINDING-M1-03** | `outbox.go` | **HIGH** | Duplicate Message Delivery on Concurrent `Flush()` Calls | Confirmed empirically |
| **FINDING-M1-04** | `pairing_code.go` | **MEDIUM** | UTF-8 Rune Slicing Boundary Insecurity in `FormatPairingCode()` | Confirmed empirically |
| **FINDING-M1-05** | `outbox.go` | **LOW/MEDIUM** | Pointer Pinning / Memory Overhead in Slice Reslicing | Confirmed logically |

---

## Detailed Empirical Findings

### FINDING-M1-01: Data Race & Interior Pointer Leak in `LoadCredentials()`
- **Severity**: **CRITICAL**
- **Affected File**: `utils/pairing_code.go`, Lines 98–121
- **Empirical Proof Test**: `TestChallenge_PairingCode_DataRace` in `utils/challenger_stress_test.go`

#### Description
`LoadCredentials()` returns a pointer `*WorkerCredentials` pointing directly to internal manager struct field `m.creds` (`return &m.creds, nil`). When callers invoke `LoadCredentials()`, they receive a reference to `m.creds`. Subsequent accesses to fields of `*WorkerCredentials` by the caller occur outside of `m.mu` lock. When another goroutine concurrently calls `SaveCredentials()`, `m.creds` is updated under lock, resulting in unsynchronized concurrent read/write operations on the same memory location.

#### Data Race Output (Go `-race` Detector)
```text
WARNING: DATA RACE
Write at 0x00c000... by goroutine A:
  utils.(*PairingCodeManager).SaveCredentials()
      d:/Documents/dca/utils/pairing_code.go:128

Previous read at 0x00c000... by goroutine B:
  utils.TestChallenge_PairingCode_DataRace.func2()
      d:/Documents/dca/utils/challenger_stress_test.go:48
```

#### Blast Radius
Credential corruption, invalid boolean state reads (`IsPaired`), or memory corruption under multi-threaded worker operation.

#### Mitigation Recommendation
Return a struct copy `WorkerCredentials` rather than a pointer `*WorkerCredentials` to internal state `&m.creds`:
```go
func (m *PairingCodeManager) LoadCredentials() (WorkerCredentials, error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    ...
    return m.creds, nil
}
```

---

### FINDING-M1-02: `Attempts` Counter Increment Lost on Delivery Failure
- **Severity**: **HIGH**
- **Affected File**: `utils/outbox.go`, Lines 86–89
- **Empirical Proof Test**: `TestChallenge_Outbox_AttemptsNotPersistedOnFailure` in `utils/challenger_stress_test.go`

#### Description
In `Outbox.Flush()`, line 83 reads `head := o.items[0]`, copying `OutboxItem` by value. Line 86 executes `head.Attempts++`. If `sendFunc(head)` fails and returns an error, `Flush()` returns `err` immediately. Because `head` was a local value copy and `o.items[0]` in the `o.items` slice was never updated with `head.Attempts`, the attempt count stored in the queue remains 0. On subsequent retry attempts, `head.Attempts` is read again as 0 and incremented to 1, causing `Attempts` to remain 0 permanently in queue storage.

#### Empirical Result
```text
Flush call #1 received item.Attempts = 1
Flush call #2 received item.Attempts = 1
Final queued item Attempts count stored in outbox = 0
FAIL: Outbox failed to persist Attempts increment on delivery failure! Stored Attempts = 0
```

#### Blast Radius
Queue items never accurately track attempt counts. Any retry limits, exponential backoff policies, or drop-after-N-retries logic relying on `item.Attempts` are completely broken.

#### Mitigation Recommendation
Increment `o.items[0].Attempts` in place within `Outbox.Flush()` under mutex lock before invoking `sendFunc`, or update `o.items[0]` upon `sendFunc` failure.

---

### FINDING-M1-03: Duplicate Message Delivery on Concurrent `Flush()` Calls
- **Severity**: **HIGH**
- **Affected File**: `utils/outbox.go`, Lines 76–98
- **Empirical Proof Test**: `TestChallenge_Outbox_ConcurrentFlushDuplicateSends` in `utils/challenger_stress_test.go`

#### Description
`Outbox.Flush()` reads `head := o.items[0]` and unlocks `o.mu` while executing `sendFunc(head)`. If two goroutines (such as `flushLoop` periodic ticker and WebSocket reconnect trigger in `WorkerDaemon`) call `Flush()` simultaneously when an item is in the outbox, both goroutines observe the same head item, release the lock, and invoke `sendFunc(head)` concurrently.

#### Empirical Result
```text
Total sendFunc calls for single item = 2
FAIL: Item sent 2 times due to concurrent Flush calls!
```

#### Blast Radius
Duplicate JSON-RPC message payloads sent over the WebSocket connection to the King node, causing duplicate command executions or response handling anomalies.

#### Mitigation Recommendation
Use an atomic flag or single-flight mutex lock in `Outbox` to ensure only one `Flush()` execution proceeds at a time.

---

### FINDING-M1-04: UTF-8 Rune Slicing Boundary Insecurity in `FormatPairingCode()`
- **Severity**: **MEDIUM**
- **Affected File**: `utils/pairing_code.go`, Lines 56–63
- **Empirical Proof Test**: Unit testing with multibyte inputs

#### Description
`FormatPairingCode()` checks string byte length `len(clean) != 6` and performs byte slicing `clean[:3] + "-" + clean[3:]`. If input contains multibyte UTF-8 characters that total 6 bytes (e.g., two 3-byte UTF-8 runes), `len(clean)` equals 6, but byte index 3 slices through the middle of a UTF-8 rune boundary, generating invalid UTF-8 bytes and corrupted output strings.

#### Blast Radius
Malformed string formatting or invalid UTF-8 byte sequences when processing non-ASCII input.

#### Mitigation Recommendation
Validate that code matches alphanumeric regex prior to formatting or perform rune-based counting and slicing using `[]rune(clean)`.

---

### FINDING-M1-05: Memory Leak Potential in `Outbox` Slice Reslicing
- **Severity**: **LOW/MEDIUM**
- **Affected File**: `utils/outbox.go`, Lines 59, 95, 110

#### Description
`o.items = o.items[1:]` removes queue elements by reslicing. In Go, reslicing does not clear the pointer/value at index 0 of the underlying array until garbage collected or reallocated. If `OutboxItem` payloads contain large byte slices (`json.RawMessage`), memory referenced by removed elements remains pinned in the underlying array capacity.

#### Mitigation Recommendation
Zero out `o.items[0] = OutboxItem{}` before reslicing `o.items = o.items[1:]`.

---

## Stress Test Results Matrix

| Stress Test Scenario | Target Component | Expected Behavior | Actual Behavior | Pass/Fail |
|----------------------|------------------|-------------------|-----------------|-----------|
| Concurrent Read/Write Credentials | `pairing_code.go` | Thread-safe operation without data races | Data race on `m.creds` pointer leak | **FAIL** |
| Outbox Attempt Counter Persist | `outbox.go` | Increment `Attempts` on failure | `Attempts` counter remains 0 | **FAIL** |
| Concurrent `Flush()` Execution | `outbox.go` | Single delivery per item | Duplicate delivery over network | **FAIL** |
| High Concurrency Buffer Overflow (50k items, 50 workers) | `outbox.go` | Bounded capacity maintained cleanly | Capacity bounded at `maxSize` (500 items) | **PASS** |
| Mock WS Drop / Reconnect (15 cycles) | `worker_daemon.go` | Auto-reconnect & resume outbox flush | Reconnects cleanly, flushes outbox items | **PASS** |
| Goroutine Leak Check (5 start/stop cycles) | `worker_daemon.go` | No leaked goroutines | Goroutines return to baseline | **PASS** |
