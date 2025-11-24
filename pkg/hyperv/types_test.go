package hyperv

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVMInfo(t *testing.T) {
	t.Run("VMInfo struct holds all fields", func(t *testing.T) {
		now := time.Now()

		info := VMInfo{
			Name:           "TestVM",
			State:          "Running",
			CPUUsage:       50,
			MemoryAssigned: 2048,
			Uptime:         "01:30:00",
			Status:         "Operating normally",
			CreationTime:   now,
			ProcessorCount: 4,
			MemoryStartup:  2048,
			Generation:     2,
		}

		assert.Equal(t, "TestVM", info.Name)
		assert.Equal(t, "Running", info.State)
		assert.Equal(t, 50, info.CPUUsage)
		assert.Equal(t, int64(2048), info.MemoryAssigned)
		assert.Equal(t, "01:30:00", info.Uptime)
		assert.Equal(t, "Operating normally", info.Status)
		assert.Equal(t, now, info.CreationTime)
		assert.Equal(t, 4, info.ProcessorCount)
		assert.Equal(t, int64(2048), info.MemoryStartup)
		assert.Equal(t, 2, info.Generation)
	})
}

func TestVMState(t *testing.T) {
	t.Run("VMState constants defined", func(t *testing.T) {
		assert.Equal(t, VMState("Off"), VMStateOff)
		assert.Equal(t, VMState("Starting"), VMStateStarting)
		assert.Equal(t, VMState("Running"), VMStateRunning)
		assert.Equal(t, VMState("Paused"), VMStatePaused)
		assert.Equal(t, VMState("Stopping"), VMStateStopping)
		assert.Equal(t, VMState("Saved"), VMStateSaved)
		assert.Equal(t, VMState("Checkpointing"), VMStateCheckpointing)
	})
}

func TestNetworkInfo(t *testing.T) {
	t.Run("NetworkInfo struct holds fields", func(t *testing.T) {
		info := NetworkInfo{
			IPAddress:  "192.168.1.100",
			MACAddress: "00:15:5D:00:00:01",
		}

		assert.Equal(t, "192.168.1.100", info.IPAddress)
		assert.Equal(t, "00:15:5D:00:00:01", info.MACAddress)
	})
}
