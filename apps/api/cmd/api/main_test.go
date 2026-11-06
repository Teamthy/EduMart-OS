package main

import "testing"

// A migration Job runs with the fleet's configuration, which says "do not migrate on boot". Honouring that setting
// inside `-migrate-only` produces a job that opens the database, finds the migrations it was created to apply, and
// exits 1. The staging rehearsal caught exactly that: a "completed" Job, three unready replicas, and a `/readyz`
// body listing every migration as pending.
func TestMigrateOnlyAlwaysMigrates(t *testing.T) {
	t.Setenv("EDUMART_MIGRATE_ON_BOOT", "false")
	if !migrateOnBootFor(true) {
		t.Fatal("-migrate-only opened the database without applying migrations; a migration job must migrate")
	}
}

// And a replica still obeys the setting, in both directions, because the race the setting prevents is real: N
// replicas booting together all see the same pending migration.
func TestReplicasObeyMigrateOnBoot(t *testing.T) {
	t.Setenv("EDUMART_MIGRATE_ON_BOOT", "false")
	if migrateOnBootFor(false) {
		t.Fatal("a replica migrated on boot despite EDUMART_MIGRATE_ON_BOOT=false")
	}
	t.Setenv("EDUMART_MIGRATE_ON_BOOT", "true")
	if !migrateOnBootFor(false) {
		t.Fatal("a replica refused to migrate on boot despite EDUMART_MIGRATE_ON_BOOT=true")
	}
	t.Setenv("EDUMART_MIGRATE_ON_BOOT", "")
	if !migrateOnBootFor(false) {
		t.Fatal("an unset EDUMART_MIGRATE_ON_BOOT must keep the documented default: migrate on boot")
	}
}
