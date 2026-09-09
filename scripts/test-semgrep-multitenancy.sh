#!/bin/sh
set -eu

semgrep_fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "$semgrep_fixture_dir"' EXIT

cat >"$semgrep_fixture_dir/unsafe.go" <<'EOF'
package fixture

func unsafe(db DB, ctx Context, appointmentID string) {
	db.Query(ctx, `SELECT id FROM appointment WHERE id = $1`, appointmentID)
}
EOF

cat >"$semgrep_fixture_dir/safe.go" <<'EOF'
package fixture

func safe(db DB, ctx Context, organizationID string) {
	db.QueryRow(
		ctx,
		`SELECT count(*) FROM patient_profile WHERE organization_id = $1`,
		organizationID,
	)
}
EOF

if semgrep \
	--config .semgrep/multitenancy.yml \
	--error \
	"$semgrep_fixture_dir/unsafe.go"; then
	echo "expected tenant rule to reject SQL without organization_id" >&2
	exit 1
fi

semgrep \
	--config .semgrep/multitenancy.yml \
	--error \
	"$semgrep_fixture_dir/safe.go"
