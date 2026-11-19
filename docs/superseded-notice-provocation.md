# Superseded-notice provocation

Throwaway fixture on a throwaway branch. It exists so a production review run
can be routed to `superseded` by moving the head while that run is in flight,
which is the only way to observe the v0.20.0 `NotifySuperseded` notice end to
end. Branch and PR are deleted after the observation.

Nothing here is intended to land.

Second commit: moves the PR head while the first review run is in flight, so
that run routes to `superseded` instead of posting a verdict at a stale head.

Third head: starts the review run that the next push will supersede.
