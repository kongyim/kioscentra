package central

import (
	"testing"
	"time"

	"kiosentra/packages/contracts"
)

func TestRotationReplayRestartAndScope(t *testing.T) {
	o := testOptions(t)
	now := time.Now().UTC()
	o.Now = func() time.Time { return now }
	s := openTest(t, o)
	old := enrollTest(t, s, o, 1)
	id := contracts.ID()
	body := contracts.Rotation{PublicKey: enrollment(o, 1).PublicKey}
	now = now.Add(24*time.Hour - time.Minute)
	var next contracts.Credential
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", old.AccessToken, id, body), 200, &next)
	if next.DeviceID != old.DeviceID || next.AccessToken == old.AccessToken || !next.ExpiresAt.After(old.ExpiresAt) {
		t.Fatal("invalid rotation")
	}
	s.Close()
	s = openTest(t, o)
	// Local old credential has expired; only the committed rotation remains replayable.
	now = now.Add(2 * time.Minute)
	var replay contracts.Credential
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", old.AccessToken, id, body), 200, &replay)
	if replay != next {
		t.Fatal("rotation changed on retry")
	}
	expect(t, request(s, "GET", "/api/v1/agent/policy", old.AccessToken, "", nil), 401, nil)
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", old.AccessToken, contracts.ID(), heartbeat(now)), 401, nil)
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", old.AccessToken, contracts.ID(), body), 401, nil)
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", old.AccessToken, id, contracts.Rotation{PublicKey: enrollment(o, 2).PublicKey}), 409, nil)
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", next.AccessToken, id, body), 200, &replay)
	expect(t, request(s, "GET", "/api/v1/agent/policy", next.AccessToken, "", nil), 200, nil)
	now = now.Add(3 * time.Minute)
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", old.AccessToken, id, body), 401, nil)
	var newest contracts.Credential
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", next.AccessToken, contracts.ID(), body), 200, &newest)
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", newest.AccessToken, id, body), 409, nil)
}

func TestRotationRollbackAndMigration(t *testing.T) {
	o := testOptions(t)
	s := openTest(t, o)
	c := enrollTest(t, s, o, 1)
	// Recreate a genuine v1 database without the new receipt table.
	if _, err := s.db.Exec("DROP TABLE credential_rotations; PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, o)
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatal("migration", version, err)
	}
	body := contracts.Rotation{PublicKey: enrollment(o, 1).PublicKey}
	id := contracts.ID()
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", c.AccessToken, "", body), 400, nil)
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", c.AccessToken, id, contracts.Rotation{PublicKey: enrollment(o, 2).PublicKey}), 400, nil)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_rotation BEFORE UPDATE ON devices BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", c.AccessToken, id, body), 503, nil)
	expect(t, request(s, "GET", "/api/v1/agent/policy", c.AccessToken, "", nil), 200, nil)
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM credential_rotations").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial receipt", count, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_rotation"); err != nil {
		t.Fatal(err)
	}
	expect(t, request(s, "POST", "/api/v1/agent/credentials/rotate", c.AccessToken, id, body), 200, nil)
}
