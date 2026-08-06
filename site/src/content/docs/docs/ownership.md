---
title: Skill ownership
description: Who may publish to a skill name — and why an upgrade changes nothing until you write a rule.
---

Ownership answers one question: who may publish to a skill name. Someone drops a change into `payments:refunds` and nobody notices until an agent runs it. Ownership rules mean a person has to say yes first.

It is not an instance role, it does not gate reads, and it is off until you write a rule.

## The one thing to know first

**An unowned name does not hold a publish.** If no rule matches, the publish goes through exactly as it did before ownership existed.

That is deliberate. Holding every unowned publish would flood the review queue on upgrade day with changes nobody asked to review. Protection switches on per namespace, when someone writes the first rule that covers it. There is no flag day, and upgrading changes nothing on its own.

## Rules and patterns

A rule is a pattern plus one or more members. A rule with no members is rejected with a 422 — it would resolve a name to "owned" with nobody able to review it, which is strictly worse than leaving it unowned.

A pattern is either an exact skill name, or a name followed by a single trailing `*`:

| Pattern | Covers | Does not cover |
|---|---|---|
| `payments:refunds` | that one name | anything else |
| `payments:*` | every name starting `payments:` | `payments` itself, `payroll:x` |
| `*` | every skill name | — |

`*` may only ever be the final character. No mid-string globs, no character classes. A grammar that fits in your head is a grammar people trust.

Two things about prefixes catch people out:

- **A prefix is a plain string prefix, not a namespace prefix.** `payments:*` is the readable case, but the `:` isn't required — `pay*` is a valid pattern, and it matches `paypal` as well as `payments:refunds`. Include the `:` unless you actually mean "every name starting with these letters".
- **`payments:*` does not match the bare name `payments`.** The scope is literally `payments:`, so a skill called exactly `payments` is a different name and needs its own rule.

## Resolution: exactly one rule wins

When you publish, skael matches the skill name against every rule and picks **one**:

1. An **exact** match wins. Nothing can be more specific.
2. Otherwise the **longest matching prefix** wins.
3. Otherwise the name is **unowned**.

**The longest match replaces — it does not stack.** Same model as CODEOWNERS and `.gitignore`. Given rules for `*`, `payments:*`, and `payments:refunds`:

| Publishing to | Resolves to | Not to |
|---|---|---|
| `payments:refunds` | the exact rule | `payments:*` or `*` |
| `payments:invoices` | `payments:*` | `*` |
| `search:index` | `*` | — |

Stacking sounds safer but breaks the feature: a namespace owner could never delegate a skill away, and delegating is the whole point of patterns.

## Who may change a rule

Any one of these is enough:

1. You are an **instance admin** (`owner` or `admin`).
2. You are a member of the rule for **that exact pattern**.
3. You are a member of a rule that **strictly contains** that pattern.

Clause 3 is load-bearing. Without it, delegation is one-way: an owner of `payments:*` who carves `payments:refunds` out to someone else stops being an owner of refunds — longest match replaces — and could never take it back.

**It only ever narrows.** A delegate cannot widen their own scope, because no rule they belong to contains the enclosing namespace. An exact pattern strictly contains nothing, so holding `payments:refunds` gives you no say over `payments:*`.

This is the entire escalation surface of ownership. It is guarded by a randomized property test rather than examples, because three individually-correct clauses can compose into a widening path that no per-clause test would see.

## First publish claims the name

Publishing **version 1** of a name that resolves to unowned records you as its sole owner, via an exact-pattern rule.

Two limits matter:

- **Unless a rule already covers the name.** Otherwise publishing `payments:anything` would be a way to make yourself an owner inside someone else's namespace.
- **Version 1 only.** Publishing v2 of a pre-existing unowned skill claims nothing. Without that, the first person to update any old skill on an upgraded instance would silently become its sole owner — and the "upgrade is a no-op" promise would be false.

A publish with no authenticated user claims nothing. If the claim fails, it is logged and the publish still succeeds: the version is already durable, and unowned is a state the product models.

Imports behave the same way — an imported skill claims its importer, on first import only.

## What a hold means, and who clears what

A hold is a **set of reasons**, not a state. Each one has to clear before the version is served. There are two:

| Reason | Means | Cleared by |
|---|---|---|
| `scan` | an appealable security finding | a verified quality score at or above `QUALITY_FLOOR`, or an instance admin |
| `ownership` | you are not an owner of the name | a skill owner, or an instance admin |

The two rules that make this worth anything:

- **A quality score clears `scan` and nothing else.** It can never clear `ownership`. If it could, the whole review path would be decorative — publish anything into any namespace, wait for a score, ship it.
- **A skill owner clears `ownership` and nothing else.** They can never clear `scan`. If they could, your security gate would only be as strong as the least careful self-managed namespace.

An instance admin can clear either. `--override` at publish time suppresses only the `scan` reason — **it does not clear an ownership hold.**

Rejecting any single reason rejects the whole version. There is no partial-reject state.

When more than one reason is outstanding you have to say which one you're deciding (`--reason-kind`). When exactly one is, it's inferred — which is what keeps existing `skael review --approve --reason "…"` commands working.

## What ownership never does

**It never gates reads.** Anyone with an account can list, search, download, and sync any skill, and can see who owns what. Ownership is about writes.

**It never re-gates a version that already shipped.** Deleting a user, removing a rule, or handing a namespace to a different team changes who reviews *future* changes and nothing else. A skill that worked yesterday keeps working for everyone who synced it.

A member whose account is deleted is skipped when listing owners. A rule that ends up empty falls back to instance admins rather than failing a publish.

One gap worth knowing: `DELETE /api/skills/{name}` has no ownership check. Any authenticated member can delete every version of a skill while being unable to publish a line to it. That is pre-existing — ownership did not introduce it — and it is a follow-up.

## Working with rules

From the CLI:

```bash
skael owners list                                    # every rule and its members
skael owners show payments:refunds                   # who owns this name, and which rule matched
skael owners set payments:* alice@acme.com bob@acme.com
skael owners add payments:* carol@acme.com
skael owners rm  payments:* bob@acme.com
skael owners delete payments:*
```

`set` replaces the member list wholesale; `add` and `rm` read the current list and write it back. Members are given as **email addresses** and resolved before anything is written — an address that matches no account aborts the command and lists near misses, so you never half-apply a rule. See the [CLI reference](/docs/cli#skael-owners).

In the dashboard: **Settings → Ownership** for the rules, and the owners card on any skill's detail page.

Over HTTP: [the ownership API](/docs/api#ownership). The API takes user IDs, not emails, and returns members hydrated with name and email.

## Turning it on for a namespace

```bash
# 1. Claim it.
skael owners set payments:* alice@acme.com bob@acme.com

# 2. Someone outside the rule publishes. Their version is held.
skael publish ./payments-refunds
#    → held for review: ownership — alice@acme.com, bob@acme.com

# 3. An owner looks at what changed.
skael review show payments:refunds

# 4. And releases it.
skael review payments:refunds 4 --approve --reason "looks right" --reason-kind ownership
```

Start with one namespace, confirm a non-member's publish is actually held, then widen. Everything you haven't written a rule for keeps working exactly as before.
