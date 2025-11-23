package hyperv

import "time"

// VMInfo represents VM information returned by Get-VM
type VMInfo struct {
	Name              string    `json:"Name"`
	State             string    `json:"State"`
	CPUUsage          int       `json:"CPUUsage"`
	MemoryAssigned    int64     `json:"MemoryAssigned"`
	Uptime            string    `json:"Uptime"`
	Status            string    `json:"Status"`
	CreationTime      time.Time `json:"CreationTime"`
	ProcessorCount    int       `json:"ProcessorCount"`
	MemoryStartup     int64     `json:"MemoryStartup"`
	Generation        int       `json:"Generation"`
}

// VMState represents Hyper-V VM states
type VMState string

const (
	VMStateOff          VMState = "Off"
	VMStateStarting     VMState = "Starting"
	VMStateRunning      VMState = "Running"
	VMStatePaused       VMState = "Paused"
	VMStateStopping     VMState = "Stopping"
	VMStateSaved        VMState = "Saved"
	VMStateCheckpointing VMState = "Checkpointing"
)

// NetworkInfo represents VM network information
type NetworkInfo struct {
	IPAddress string
	MACAddress string
}
