package workers_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/workers"
)

func TestReadiness_NamesTheOddImageAndTheOfflineWorker(t *testing.T) {
	now := time.Now()
	w := func(id, image string) workers.Worker {
		return workers.Worker{WorkersCheckin: workers.WorkersCheckin{WorkerID: id, SandboxImage: image}, LastSeen: now}
	}
	list := []workers.Worker{
		w("a", "base@1"),
		w("b", "base@1"),
		w("c", "base@2"),
		{WorkersCheckin: workers.WorkersCheckin{WorkerID: "d"}, LastSeen: now.Add(-time.Hour)},
	}
	workers.Readiness(list, now)

	require.Empty(t, list[0].Problems)
	require.Empty(t, list[1].Problems)
	require.Len(t, list[2].Problems, 1)
	require.Contains(t, list[2].Problems[0], "sandbox image base@2 differs")
	require.Contains(t, list[3].Problems[0], "offline")
}
