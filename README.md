# Sente

Go — cờ vây — on iOS: a SwiftUI app and a Go backend, built as one project.
Full design documents live in [`docs/`](docs/README.md) (written in Vietnamese).

Currently in TestFlight beta, running against a deployed server.

## What it does

- **Play online** — invite a friend by link or QR code, or scan theirs. Live games
  with byo-yomi, and correspondence games measured in days per move, with push
  notifications when it is your turn or your clock runs low.
- **Friends** — add by an eight-character friend code or by scanning a QR, then
  invite straight into a game. Requires Sign in with Apple; guest accounts play
  but cannot befriend.
- **Play offline** — four bot levels, the top two a real MCTS-UCT search that
  understands life-and-death shapes, plus pass-and-play on one device and a
  bot-versus-bot viewer.
- **Learn** — 28 interactive lessons across 4 chapters, from the rules to
  life and death, and 12 daily tsumego on a rotation with a streak.
- **Review** — step through any finished game, ask the bot for the move it would
  have played, or have it grade every move and name the three worst.
- **Vietnamese and English**, switchable in the app. Server error messages are
  localised as well, following the device language.

## The thing worth knowing

There are **two** rules engines — Swift on the client so a stone lands within a
frame, Go on the server because the server is the referee. Them disagreeing is
the single largest risk in the project. What keeps them honest is `rules-spec/`:
one constants table, one set of vectors, one parity file, run in both CI suites.
See [ADR-002](docs/03-solution-design.md#adr-002--nơi-đặt-engine-luật-cờ).

Three layers of constraint, weakest to strongest:

1. **43 conformance vectors** across 9 categories — written by hand from the
   specification, run by both engines.
2. **36 parity positions** — the hash of a final position, which catches one
   engine changing how it hashes.
3. **24 game traces** — 3,936 hash checks *per move*, which catch a divergence at
   the exact move it happens.

The second risk — **two nodes owning one game** — is held off by a Redis lease
with compare-and-swap, and demonstrated by a chaos test in `internal/node`: kill
a node mid-game, another rebuilds it from the database, and the position and
prisoner counts have to match exactly.

Any rules bug found **must** become a new vector **before** it is fixed. That is
a process, not a tool — nothing can enforce it automatically.

## Layout

```
docs/           design: requirements, rules spec, architecture, API, roadmap
rules-spec/     the contract shared by the two rules engines
  SCHEMA.md             what a vector file must contain
  zobrist_table.json    hash constants - changing one is a breaking change
  vectors/              conformance vectors, written by hand from the spec
  parity/
    positions.json      36 positions + hashes, against regressions
    games.json          24 games recorded move by move, differential
  tools/                scripts that generate the Zobrist table and vectors
sente-ios/      the iOS app - project.yml (xcodegen) produces Sente.xcodeproj
  Packages/GoKit        rules engine, plain Swift, no dependencies
  Packages/SenteNet     REST + WebSocket, Keychain, backoff
  Packages/SenteUI      BoardView (Canvas), tokens, clocks, banners
  App/Core              session, router, language, push, haptics
  App/Features          Game, Home, Friends, Scan, Invite, Learn, Bot, Local, Settings
  Widgets/              WidgetKit extension - the "your turn" widget
  Tests/                app-level tests
  scripts/render-icon.swift   draws the icon with CoreGraphics, light and dark
sente-server/   the Go backend
  internal/rules        rules engine, a pure package with no I/O
  internal/game         clocks (4 formats), dead-stone negotiation,
                        GameSession - the pure state machine of one game
  internal/store        PostgreSQL: schema, migrations, game storage
  internal/cluster      Redis leases: which node owns which game
  internal/node         registry: adopt a game, rebuild from the DB, release
  internal/wire         command/event codec between nodes
  internal/hub          routing: run it here or forward it
  internal/auth         guest accounts + JWT
  internal/apple        Sign in with Apple: JWKS, nonce, revocation
  internal/httpapi      REST + WebSocket gateway, invitations, friends
  internal/notify       turns game events into pushes
  internal/push         APNs client, token-based auth
  internal/sweep        times out games no node is running
  internal/ratelimit    token bucket shared across nodes
  internal/metrics      Prometheus counters
  cmd/server            the binary that runs one node
deploy/         dev compose (Postgres + Redis) and a production compose
scripts/        quality gates that run the same locally and in CI
```

## Running it

Needs Go 1.25, Xcode 26 (iOS 17.0 deployment target), Python 3 for the quality
gates, Docker for the integration suites, and xcodegen for the app.

```bash
make ci           # everything a pull request must pass, run locally
make test         # both engine suites plus the database integration tests
make test-fast    # the same, minus anything that needs Docker
make test-ios     # the Swift packages
make test-server  # the Go suites
make app          # build the iOS app on the simulator and run its unit tests
make db-up        # start PostgreSQL and Redis for local development
make run          # run the server locally against db-up
make image        # build the production container image
make smoke URL=https://sente.example.com   # check a deployed instance end to end
make perf         # run the Swift suite in release, where the timings mean something
make spec         # regenerate the Zobrist table and conformance vectors

make cover        # gates: rules engines 95% (Swift also 90% of branches),
                  # everything else 80%
make drift        # fail if a generated file was edited by hand
make parity       # fail if the two engines carry different Zobrist constants
make testflight   # archive and upload to TestFlight (needs the App Store Connect key)
```

Secrets are never in git — see [docs/11](docs/11-deployment.md) for how a
deployment is configured.
