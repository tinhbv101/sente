#!/usr/bin/env bash
# Archive the iOS app and upload it to TestFlight without opening Xcode.
#
#   ASC_KEY_ID=ABC123DEFG ASC_ISSUER_ID=<uuid> ASC_KEY_FILE=deploy/secret/AuthKey_ABC123DEFG.p8 \
#     scripts/testflight.sh
#
# The App Store Connect API key comes from App Store Connect → Users and Access →
# Integrations → Team Keys (role: App Manager). Automatic signing creates or
# reuses the Apple Distribution certificate and App Store profile on its own.
# The build number is the commit count, so every upload is higher than the last.
set -euo pipefail

: "${ASC_KEY_ID:?set ASC_KEY_ID (App Store Connect API key id)}"
: "${ASC_ISSUER_ID:?set ASC_ISSUER_ID (App Store Connect issuer id)}"
: "${ASC_KEY_FILE:?set ASC_KEY_FILE (path to the AuthKey_<id>.p8 file)}"
KEY_FILE=$(cd "$(dirname "$ASC_KEY_FILE")" && pwd)/$(basename "$ASC_KEY_FILE")
[ -r "$KEY_FILE" ] || { echo "cannot read $KEY_FILE" >&2; exit 1; }

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT/sente-ios"
BUILD=$(git rev-list --count HEAD)
VERSION=$(sed -n 's/.*MARKETING_VERSION: "\(.*\)"/\1/p' project.yml)
OUT="$ROOT/sente-ios/build/testflight"
rm -rf "$OUT" && mkdir -p "$OUT"
AUTH=(-allowProvisioningUpdates -authenticationKeyPath "$KEY_FILE"
      -authenticationKeyID "$ASC_KEY_ID" -authenticationKeyIssuerID "$ASC_ISSUER_ID")

echo "Sente $VERSION ($BUILD) → TestFlight"
xcodegen generate >/dev/null

xcodebuild archive \
  -project Sente.xcodeproj -scheme Sente -configuration Release \
  -destination 'generic/platform=iOS' \
  -archivePath "$OUT/Sente.xcarchive" \
  CURRENT_PROJECT_VERSION="$BUILD" \
  "${AUTH[@]}" | grep -E "error:|warning: .*(entitlement|signing)|ARCHIVE (SUCCEEDED|FAILED)" || true
[ -d "$OUT/Sente.xcarchive" ] || { echo "archive failed" >&2; exit 1; }

cat > "$OUT/ExportOptions.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>method</key><string>app-store-connect</string>
  <key>destination</key><string>upload</string>
  <key>signingStyle</key><string>automatic</string>
  <key>teamID</key><string>H4XS3XWJ9N</string>
  <key>uploadSymbols</key><true/>
  <key>manageAppVersionAndBuildNumber</key><false/>
</dict></plist>
PLIST

xcodebuild -exportArchive \
  -archivePath "$OUT/Sente.xcarchive" \
  -exportOptionsPlist "$OUT/ExportOptions.plist" \
  -exportPath "$OUT/export" \
  "${AUTH[@]}" | grep -E "error:|Upload|EXPORT (SUCCEEDED|FAILED)" || true

echo
echo "Uploaded $VERSION ($BUILD). App Store Connect → TestFlight shows it after processing (10–30 min)."
