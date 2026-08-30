.PHONY: help spec drift parity test test-ios test-server test-fast cover ci db-up db-down \
        run image smoke app perf clean

help:
	@echo "ci            everything a pull request must pass, run locally"
	@echo "test          both engine suites plus the database integration tests"
	@echo "test-fast     the same, minus anything that needs Docker"
	@echo "db-up         start PostgreSQL and Redis for local development"
	@echo "spec          regenerate the Zobrist tables and conformance vectors"
	@echo "drift         fail if a generated file was edited by hand"
	@echo "parity        fail if the two engines carry different Zobrist constants"
	@echo "cover         enforce the coverage gates from docs/09 §7.3"
	@echo "run           run the server locally against db-up"
	@echo "image         build the production container image"
	@echo "smoke         check a deployed instance end to end: make smoke URL=https://..."
	@echo "app           build the iOS app on the simulator and run its unit tests (needs xcodegen)"
	@echo "perf          run the Swift suite in release, where the timings mean something"

spec:
	python3 rules-spec/tools/gen_zobrist.py
	python3 rules-spec/tools/gen_vectors.py

drift:
	python3 rules-spec/tools/check_drift.py

parity:
	python3 scripts/check_zobrist_parity.py

test: test-ios test-server

# Integration tests start their own throwaway PostgreSQL, so this needs Docker.
test-server:
	@if command -v go >/dev/null 2>&1; then \
		cd sente-server && gofmt -l . | tee /dev/stderr | (! read) && go vet ./... && go test ./... -race ; \
	else \
		echo "skipping: go is not installed (brew install go)"; \
	fi

test-fast:
	cd sente-server && go test ./... -short
	$(MAKE) test-ios

# Every Swift package; the app itself needs a simulator and is built by `app`.
test-ios:
	cd sente-ios/Packages/GoKit && swift test
	cd sente-ios/Packages/SenteNet && swift test
	cd sente-ios/Packages/SenteUI && swift test

# Regenerates the Xcode project, builds the app for the simulator and runs its unit
# tests (GameStore). Signed ad hoc: an unsigned build has no entitlements and the
# Keychain refuses it.
testflight: ## Archive and upload the iOS app to TestFlight (needs ASC_KEY_ID, ASC_ISSUER_ID, ASC_KEY_FILE)
	scripts/testflight.sh

app:
	cd sente-ios && xcodegen generate && xcodebuild test -project Sente.xcodeproj -scheme Sente \
	  -destination 'platform=iOS Simulator,name=iPhone 17' -derivedDataPath .build/derived \
	  CODE_SIGN_IDENTITY="-" CODE_SIGNING_REQUIRED=NO -only-testing:SenteTests 2>&1 \
	  | grep -E "error:|Executed [0-9]+ tests|TEST (SUCCEEDED|FAILED)|BUILD FAILED"

cover:
	python3 scripts/coverage_gate.py all

ci: drift parity test cover
	@echo "\nCI passed."

db-up:
	docker compose -f deploy/docker-compose.yml up -d --wait

db-down:
	docker compose -f deploy/docker-compose.yml down

# A throwaway secret: fine locally, never anywhere else.
run: db-up
	cd sente-server && \
	SENTE_DATABASE_URL="postgres://sente:sente@localhost:5432/sente?sslmode=disable" \
	SENTE_REDIS_URL="redis://localhost:6379" \
	SENTE_JWT_SECRET="0123456789abcdef0123456789abcdef" \
	SENTE_NODE_ID="local-1" \
	SENTE_PUBLIC_URL="http://localhost:8080" \
	go run ./cmd/server

image:
	docker build -t sente-server:$${VERSION:-dev} --build-arg VERSION=$${VERSION:-dev} sente-server

smoke:
	./scripts/smoke.sh $${URL:-http://localhost:8080}

perf:
	cd sente-ios/Packages/GoKit && swift test -c release 2>&1 | grep "per move"

clean:
	cd sente-ios && rm -rf .build Packages/*/.build Sente.xcodeproj
	rm -f sente-server/.coverage.out
