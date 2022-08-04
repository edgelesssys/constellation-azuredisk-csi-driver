# Azure Disk CSI Driver Limitations

- No support for Windows nodes, as Constellation only runs on Linux
- Attaching a managed disk to multiple workloads (Azure shared disk) is not supported
- Volume expansion is not supported for integrity supported disks
