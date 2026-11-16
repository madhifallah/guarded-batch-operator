package guarded_test

import rego.v1
import data.guarded.allow

fresh := {
    "now_unix": 1794826800,
    "facts": {"backup": {"succeeded": true, "completedAt": "2026-11-16T10:00:00Z"}, "maintenanceFreeze": false},
    "job": {"activeDeadlineSeconds": 120},
}

test_fresh_backup_allows if { allow with input as fresh }
test_missing_evidence_denies if { not allow with input as {} }
test_failed_backup_denies if { not allow with input as fresh with input.facts.backup.succeeded as false }
test_freeze_denies if { not allow with input as fresh with input.facts.maintenanceFreeze as true }
test_stale_backup_denies if { not allow with input as fresh with input.now_unix as 1795000000 }
