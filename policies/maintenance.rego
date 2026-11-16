package guarded

import rego.v1

default allow := false

# The controller supplies current time and trusted producer evidence.
allow if {
    input.facts.backup.succeeded == true
    completed := time.parse_rfc3339_ns(input.facts.backup.completedAt) / 1000000000
    age := input.now_unix - completed
    age >= 0
    age <= 86400
    input.facts.maintenanceFreeze == false
    input.job.activeDeadlineSeconds <= 600
}
