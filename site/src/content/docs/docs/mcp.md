---
title: MCP server
description: Connect a coding agent to the registry through read-only MCP tools.
---

Skael serves four read-only tools over the Model Context Protocol. An agent uses them to search skills, read a skill, list sets and resolve a set.

## Connect a coding agent

Create an API key. Then add the server to Claude Code:

```bash
claude mcp add --transport http skael https://HOST/mcp --header "X-API-Key: sk-..."
```

Replace `HOST` with your Skael host. Run `/mcp` in Claude Code to see the connection and the tools.

## Tools

| Tool | Scope | Arguments | Result |
|---|---|---|---|
| `search_skills` | `skills:read` | `query`, `limit` (1 to 100, default 20) | The matching skills |
| `get_skill` | `skills:read` | `name` | The latest released description and content, and metadata for each version |
| `list_sets` | `sets:read` | none | The sets the key can read |
| `resolve_set` | `sets:read` | `name`, `include_excluded` | The exact versions and digests, and `complete` |

`get_skill` never returns the content of a held version. It lists a held version with its state and hold reasons only.

`resolve_set` stores the resolution and records the key as a caller of the set. It gives the same result as `GET /api/sets/{name}/resolve`.

## Keys and scopes

The server checks `X-API-Key` with the same authentication as the HTTP API.

- A personal key with no scopes reaches all four tools.
- A scoped key needs `skills:read` or `sets:read` to reach `/mcp`. Each tool then checks its own scope.
- A set-limited key can use `list_sets` and `resolve_set` for its one set. It cannot use `search_skills` or `get_skill`.

A missing or revoked key gets HTTP 401. A key with neither scope gets HTTP 403. Every caller that reaches `/mcp` sees all four tools in `tools/list`. A call without the scope returns JSON-RPC error `-32001`, with a message such as `forbidden: this key lacks sets:read`.

## Transport

- `POST /mcp` takes one JSON-RPC 2.0 message and returns `application/json`. The server keeps no session state.
- `GET /mcp` returns 405, because the server has no event stream.
- The protocol version is `2025-06-18`. The server does not accept JSON-RPC batches.
- A request with an `Origin` header must come from the same host or from `CORS_ORIGINS`.

## Discovery

`GET /.well-known/mcp.json` is public. It gives the server name and version, the transport URL, the authentication header and the tools. It contains no registry data.

The transport URL uses the request's `Host` header. The server reads `X-Forwarded-Proto` only from an address in `TRUSTED_PROXIES`. A proxy must pass the original `Host` for this URL to be correct.
