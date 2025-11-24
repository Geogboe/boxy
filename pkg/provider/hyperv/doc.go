// Package hyperv implements a Provider backed by Windows Hyper-V using
// PowerShell and PowerShell Direct.
//
// # Overview
//
// The Hyper-V provider manages virtual machines on a Windows host through the
// Hyper-V APIs and the host-side PowerShell provider. It supports VM lifecycle
// operations (create, destroy, snapshoting, power control), as well as
// executing commands in guests using PowerShell Direct (`pkg/hyperv/psdirect`).
//
// # Requirements
//
// The Hyper-V provider requires a Windows host with Hyper-V enabled. Some
// operations require PowerShell and Hyper-V feature support and appropriate
// permissions. Use the provider only where Hyper-V is available.
//
// # API Notes
//
// This provider is responsible for mapping Boxy's ResourceSpec and Resource
// lifecycle operations to Hyper-V operations. It uses an encryptor for secret
// material and integrates with the host PowerShell executor to perform
// comfortably script-driven tasks.
//
// # Security
//
// Sensitive strings such as generated VM credentials are encrypted before
// storage. The provider favors safe string escaping and avoids interpolating
// untrusted values into generated PowerShell snippets.
//
// # Dependencies
//
// The package depends on `pkg/powershell`, `pkg/hyperv/psdirect` and the host
// environment. It should only be used on Windows hosts where the required
// services and privileges are available.
package hyperv
