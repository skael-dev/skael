---
title: Webhooks
description: Signed events for skill releases, set changes and submission decisions, with verification examples.
---

Skael sends a signed HTTP POST to an endpoint when a skill version is released, a held version is approved, a set's resolution changes or a submission is decided. A system that caches a resolved set learns of a change and does not need to poll.

## Register an endpoint

Only an instance owner or admin manages endpoints, because an endpoint receives data about every matching skill.

```bash
skael webhooks create \
  --name ci-cache \
  --url https://ci.example.com/skael \
  --event set.changed \
  --set review-tools
```

The command shows the secret once. Store it in the receiver's configuration. `--set` applies only to `set.changed`, and with no `--set` the endpoint receives `set.changed` for every set.

Send a test event, then read the delivery log:

```bash
skael webhooks test ci-cache
skael webhooks deliveries ci-cache
```

The [CLI reference](/docs/cli#skael-webhooks) lists every command, and the [HTTP API](/docs/api#webhooks) lists the routes.

## Event types

| Type | When it fires |
|---|---|
| `skill.version.published` | A publish releases a version at once, or an approved submission does. A publish the gate holds sends nothing, because a held version has nothing to serve. |
| `skill.version.approved` | A held version is released, by a review or by a verified score. |
| `set.changed` | A set's resolution id differs from the last id. Each change below recomputes the id in its own transaction, and an equal id sends nothing. |
| `submission.decided` | A person approves or rejects a submission. |

These changes can move a resolution id:

- a release that moves `latest` for a skill in the set
- an approval that releases a pinned version
- an edit of the set's entries
- a delete of a skill in the set

A release of a skill in no set sends no `set.changed`. A change to a set's description sends nothing. Creating a set records its first id and sends nothing. Deleting a set sends nothing.

The server also has the type `webhook.test`. Only the test route sends it, to one endpoint, and an endpoint cannot subscribe to it.

## Payloads

Every delivery has this body. `id` is the event id, and `time` is when the change happened.

| Field | Type | Description |
|---|---|---|
| `id` | string | The event id. It is the same on each attempt and each redelivery |
| `type` | string | The event type |
| `time` | string | RFC 3339 time in UTC |
| `data` | object | The fields for this event type |

A payload holds ids, names, versions and digests. It never holds skill content or a rejection's reason, so a webhook cannot go around a set-limited key.

The server stores the body as JSONB, so the key order and the spaces can differ from these examples. Parse the JSON and do not depend on its text.

### skill.version.published and skill.version.approved

```json
{
  "id": "5a1f0c52-7c55-4c0e-9d0e-3f6f5a0a9b11",
  "type": "skill.version.published",
  "time": "2026-11-02T10:15:00.123456Z",
  "data": {
    "skill": "lint",
    "version": 7,
    "sha256": "ab12..."
  }
}
```

### set.changed

```json
{
  "id": "0b6e2c7e-2f7d-4a44-8d29-6f2d7f1c3a90",
  "type": "set.changed",
  "time": "2026-11-02T10:15:00.123456Z",
  "data": {
    "set_id": "c7a1f6de-93a1-4a55-8a3c-1d2e3f4a5b6c",
    "set": "review-tools",
    "resolution_id": "9f2c...",
    "complete": true,
    "skills": [
      {"name": "lint", "version": 7, "sha256": "ab12...", "scan_verdict": "clean", "approval_state": "released"}
    ]
  }
}
```

`complete` is `false` when the resolution excludes an entry. `skills` lists only the entries the resolution includes.

### submission.decided

```json
{
  "id": "e3d0a8f4-61b7-4f3a-b6f1-2c9a7d5e8f01",
  "type": "submission.decided",
  "time": "2026-11-02T10:15:00.123456Z",
  "data": {
    "submission_id": "7d3c9a10-4b2e-4f6a-9c1d-8e7f6a5b4c3d",
    "skill": "lint",
    "state": "approved",
    "consumer": "ci",
    "version": 8
  }
}
```

`state` is `approved` or `rejected`. `version` is present only on an approval. Read a rejection's reason from `GET /api/submissions/{id}`.

### webhook.test

```json
{
  "id": "1c9e7b3a-5d2f-4e8c-a0b1-7f6e5d4c3b2a",
  "type": "webhook.test",
  "time": "2026-11-02T10:15:00.123456Z",
  "data": {"message": "This is a test event from Skael."}
}
```

## Headers

| Header | Value |
|---|---|
| `X-Skael-Event-Id` | The event id, equal to `id` in the body |
| `X-Skael-Event-Type` | The event type, equal to `type` in the body |
| `X-Skael-Timestamp` | Unix seconds when this attempt was signed |
| `X-Skael-Signature` | One `v1=<hex>` for each live secret, separated by commas |

## Verify the signature

1. Read the raw request body. Do not parse it and encode it again.
2. Compute the hex HMAC-SHA256 of `<timestamp>.<raw body>`, with the endpoint's secret as the key. The secret includes its `whsec_` prefix.
3. Compare the result with each `v1=` value in `X-Skael-Signature`, in constant time. Any match verifies.
4. Reject a timestamp that is too old. These examples allow 5 minutes. Each attempt has a new timestamp, so a retry passes this check.

Each example is a complete server. It reads `WEBHOOK_SECRET` and `PORT`, returns 204 for a verified delivery and returns 401 for any other request.

### Go

```go
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const tolerance = 5 * 60

func verify(secret, timestamp, header string, body []byte) bool {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	age := time.Now().Unix() - ts
	if age > tolerance || age < -tolerance {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, part := range strings.Split(header, ",") {
		got, ok := strings.CutPrefix(strings.TrimSpace(part), "v1=")
		if ok && hmac.Equal([]byte(got), []byte(want)) {
			return true
		}
	}
	return false
}

func main() {
	secret := os.Getenv("WEBHOOK_SECRET")
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || !verify(secret, r.Header.Get("X-Skael-Timestamp"), r.Header.Get("X-Skael-Signature"), body) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Println("verified", r.Header.Get("X-Skael-Event-Type"), r.Header.Get("X-Skael-Event-Id"))
		w.WriteHeader(http.StatusNoContent)
	})
	if err := http.ListenAndServe(":"+os.Getenv("PORT"), nil); err != nil {
		panic(err)
	}
}
```

### Python

```python
import hashlib
import hmac
import os
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

SECRET = os.environ["WEBHOOK_SECRET"].encode()
TOLERANCE = 5 * 60


def verify(timestamp, header, body):
    try:
        if abs(time.time() - int(timestamp)) > TOLERANCE:
            return False
    except (TypeError, ValueError):
        return False
    want = hmac.new(SECRET, timestamp.encode() + b"." + body, hashlib.sha256).hexdigest()
    for part in (header or "").split(","):
        part = part.strip()
        if part.startswith("v1=") and hmac.compare_digest(part[3:].encode(), want.encode()):
            return True
    return False


class Receiver(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        if verify(self.headers.get("X-Skael-Timestamp"), self.headers.get("X-Skael-Signature"), body):
            print("verified", self.headers["X-Skael-Event-Type"], self.headers["X-Skael-Event-Id"], flush=True)
            self.send_response(204)
        else:
            self.send_response(401)
        self.end_headers()


HTTPServer(("", int(os.environ["PORT"])), Receiver).serve_forever()
```

### Node

```js
const crypto = require("node:crypto");
const http = require("node:http");

const secret = process.env.WEBHOOK_SECRET;
const tolerance = 5 * 60;

function verify(timestamp, header, body) {
  const ts = Number(timestamp);
  if (!timestamp || !Number.isInteger(ts) || Math.abs(Date.now() / 1000 - ts) > tolerance) {
    return false;
  }
  const want = Buffer.from(
    crypto.createHmac("sha256", secret).update(`${timestamp}.`).update(body).digest("hex"),
  );
  return (header || "").split(",").some((part) => {
    const value = part.trim();
    if (!value.startsWith("v1=")) return false;
    const got = Buffer.from(value.slice(3));
    return got.length === want.length && crypto.timingSafeEqual(got, want);
  });
}

http
  .createServer((req, res) => {
    const chunks = [];
    req.on("data", (chunk) => chunks.push(chunk));
    req.on("end", () => {
      const h = req.headers;
      if (verify(h["x-skael-timestamp"], h["x-skael-signature"], Buffer.concat(chunks))) {
        console.log("verified", h["x-skael-event-type"], h["x-skael-event-id"]);
        res.statusCode = 204;
      } else {
        res.statusCode = 401;
      }
      res.end();
    });
  })
  .listen(Number(process.env.PORT));
```

## Rotate a secret

```bash
skael webhooks rotate ci-cache --window-hours 48
```

The command shows the new secret once. During the window the server signs each delivery with both secrets, so `X-Skael-Signature` holds two `v1=` values and a receiver with either secret verifies. The default window is 24 hours and the maximum is 168. `--window-hours 0` ends the old secret at once.

Put the new secret in the receiver before the window ends.

## Delivery

Delivery is at least once, and the order is not guaranteed. A receiver can get one event twice, and it can get an older event after a newer one. So:

- Deduplicate on the event id.
- For `set.changed`, compare `resolution_id` with the id you hold. If they differ, resolve the set again with `GET /api/sets/{name}/resolve`. Do not treat the newest delivery as the current state.

A delivery succeeds when the receiver returns a 2xx status within 15 seconds. The server does not store the response body.

## Retries

Any other result makes the server retry. The server makes 24 attempts in about 24 hours. The wait doubles from one minute to 80 minutes, and then stays at 80 minutes:

| After attempt | Wait |
|---|---|
| 1 | 1 minute |
| 2 | 2 minutes |
| 3 | 4 minutes |
| 4 | 8 minutes |
| 5 | 16 minutes |
| 6 | 32 minutes |
| 7 | 64 minutes |
| 8 to 23 | 80 minutes |

After attempt 24 the delivery state is `failed`. The states are `pending`, `delivered` and `failed`, and `skael webhooks deliveries` shows each attempt.

Send a delivery again with its delivery id:

```bash
skael webhooks redeliver <delivery-id>
```

A redelivery has the same event id and the same body, a new timestamp and a new signature. It starts a new series of 24 attempts.

A deleted endpoint receives nothing further, and neither does one disabled with `"enabled": false` on `PUT /api/webhooks/endpoints/{id}`. Their queued retries stop.

## Address rule

The server refuses an address that a delivery must not reach:

- A URL must use `https`.
- The server refuses private, loopback and link-local addresses, and also shared address space (`100.64.0.0/10`), multicast and unspecified addresses.
- The server applies the rule when you save an endpoint. It applies the rule again on the resolved IP at connection time and on each redirect, so a DNS change or a redirect cannot reach a refused address.
- The server follows at most 5 redirects. A 301, 302 or 303 turns the POST into a GET with no body, and a 2xx reply to that GET marks the delivery `delivered`. So register the final address. A 307 or 308 keeps the POST and the body.
- A delivery ignores `HTTP_PROXY` and `HTTPS_PROXY`, because a proxy would hide the target from this check.

`WEBHOOK_ALLOW_PRIVATE=true` opens private addresses, loopback, shared address space and `http`, for an install whose receiver is on its own network. Link-local addresses stay refused, because they hold cloud metadata services. Multicast and unspecified addresses also stay refused.
