/*
Copyright (c) Edgeless Systems GmbH

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, version 3 of the License.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.

This file incorporates work covered by the following copyright and
permission notice:


Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package azuredisk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"sigs.k8s.io/azuredisk-csi-driver/pkg/optimization"
	volumehelper "sigs.k8s.io/azuredisk-csi-driver/pkg/util"
	azcache "sigs.k8s.io/cloud-provider-azure/pkg/cache"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/edgelesssys/constellation/csi/cryptmapper"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"
	consts "sigs.k8s.io/azuredisk-csi-driver/pkg/azureconstants"
	"sigs.k8s.io/azuredisk-csi-driver/pkg/azureutils"
)

const (
	defaultLinuxFsType              = "ext4"
	defaultWindowsFsType            = "ntfs"
	defaultAzureVolumeLimit         = 16
	volumeOperationAlreadyExistsFmt = "An operation with the given Volume ID %s already exists"
)

func getDefaultFsType() string {
	if runtime.GOOS == "windows" {
		return defaultWindowsFsType
	}

	return defaultLinuxFsType
}

// NodeStageVolume mount disk device to a staging path
func (d *Driver) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	diskURI := req.GetVolumeId()
	if len(diskURI) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	diskName, err := d.getVolumeName(diskURI)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Unable to parse disk URI: %v", err)
	}

	target := req.GetStagingTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not provided")
	}

	params := req.GetVolumeContext()
	maxShares, err := azureutils.GetMaxShares(params)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "MaxShares value not supported")
	}

	if !azureutils.IsValidVolumeCapabilities([]*csi.VolumeCapability{volumeCapability}, maxShares) {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not supported")
	}

	if acquired := d.volumeLocks.TryAcquire(diskURI); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, diskURI)
	}
	defer d.volumeLocks.Release(diskURI)

	lun, ok := req.PublishContext[consts.LUN]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "lun not provided")
	}

	source, err := d.getDevicePathWithLUN(lun)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to find disk on lun %s. %v", lun, err)
	}

	// If perf optimizations are enabled
	// tweak device settings to enhance performance
	if d.getPerfOptimizationEnabled() {
		profile, accountType, diskSizeGibStr, diskIopsStr, diskBwMbpsStr, deviceSettings, err := optimization.GetDiskPerfAttributes(req.GetVolumeContext())
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to get perf attributes for %s. Error: %v", source, err)
		}

		if d.getDeviceHelper().DiskSupportsPerfOptimization(profile, accountType) {
			if err := d.getDeviceHelper().OptimizeDiskPerformance(d.getNodeInfo(), source, profile, accountType,
				diskSizeGibStr, diskIopsStr, diskBwMbpsStr, deviceSettings); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to optimize device performance for target(%s) error(%s)", source, err)
			}
		} else {
			klog.V(2).Infof("NodeStageVolume: perf optimization is disabled for %s. perfProfile %s accountType %s", source, profile, accountType)
		}
	}

	mnt, err := d.ensureMountPoint(target)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "could not mount target %q: %v", target, err)
	}
	if mnt {
		klog.V(2).Infof("NodeStageVolume: already mounted on target %s", target) // if volume is already mounted, we also already opened the crypt device
		return &csi.NodeStageVolumeResponse{}, nil
	}

	// Get fsType and mountOptions that the volume will be formatted and mounted with
	fstype := getDefaultFsType()
	options := []string{}
	if mnt := volumeCapability.GetMount(); mnt != nil {
		if mnt.FsType != "" {
			fstype = mnt.FsType
		}
		options = append(options, collectMountOptions(fstype, mnt.MountFlags)...)
	}

	volContextFSType := azureutils.GetFStype(req.GetVolumeContext())
	if volContextFSType != "" {
		// respect "fstype" setting in storage class parameters
		fstype = volContextFSType
	}

	// If partition is specified, should mount it only instead of the entire disk.
	if partition, ok := req.GetVolumeContext()[consts.VolumeAttributePartition]; ok {
		source = source + "-part" + partition
	}

	// [Edgeless] Map the device as a crypt device, creating a new LUKS partition if needed
	fstype, integrity := cryptmapper.IsIntegrityFS(fstype)
	devicePathReal, err := d.evalSymLinks(source)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("could not evaluate device path for device %q: %v", devicePathReal, err))
	}
	devicePath, err := d.cryptMapper.OpenCryptDevice(ctx, source, diskName, integrity)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("NodeStageVolume failed on volume %v to %s, open crypt device failed: %v", source, target, err))
	}

	if blk := volumeCapability.GetBlock(); blk != nil {
		// Noop for Block NodeStageVolume
		klog.V(4).Infof("NodeStageVolume succeeded on %s to %s, capability is block so this is a no-op", diskURI, target)
		return &csi.NodeStageVolumeResponse{}, nil
	}

	// FormatAndMount will format only if needed
	klog.V(2).Infof("NodeStageVolume: formatting %s and mounting at %s with mount options(%s)", devicePath, target, options)
	if err := d.formatAndMount(devicePath, target, fstype, options); err != nil {
		return nil, status.Errorf(codes.Internal, "could not format %s(lun: %s), and mount it at %s, failed with %v", devicePath, lun, target, err)
	}
	klog.V(2).Infof("NodeStageVolume: format %s and mounting at %s successfully.", devicePath, target)

	var needResize bool
	if required, ok := req.GetVolumeContext()[consts.ResizeRequired]; ok && strings.EqualFold(required, consts.TrueValue) {
		needResize = true
	}
	if !needResize {
		if needResize, err = needResizeVolume(devicePath, target, d.mounter); err != nil {
			klog.Errorf("NodeStageVolume: could not determine if volume %s needs to be resized: %v", diskURI, err)
		}
	}

	// if resize is required, resize filesystem
	if needResize {
		klog.V(2).Infof("NodeStageVolume: fs resize initiating on target(%s) volumeid(%s)", target, diskURI)
		if err := resizeVolume(devicePath, target, d.mounter); err != nil {
			return nil, status.Errorf(codes.Internal, "NodeStageVolume: could not resize volume %s (%s):  %v", devicePath, target, err)
		}
		klog.V(2).Infof("NodeStageVolume: fs resize successful on target(%s) volumeid(%s).", target, diskURI)
	}
	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume unmount disk device from a staging path
func (d *Driver) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}
	diskName, err := d.getVolumeName(volumeID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Unable to parse disk URI: %v", err)
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	klog.V(2).Infof("NodeUnstageVolume: unmounting %s", stagingTargetPath)
	err = CleanupMountPoint(stagingTargetPath, d.mounter, true /*extensiveMountPointCheck*/)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount staging target %q: %v", stagingTargetPath, err)
	}

	// [Edgeless] Unmap the crypt device so we can properly remove the device from the node
	if err := d.cryptMapper.CloseCryptDevice(diskName); err != nil {
		return nil, status.Errorf(codes.Internal, "NodeUnstageVolume failed to close mapped crypt device for disk %s: %v", stagingTargetPath, err)
	}

	klog.V(2).Infof("NodeUnstageVolume: unmount %s successfully", stagingTargetPath)

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume mount the volume from staging to target path
func (d *Driver) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in the request")
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}

	params := req.GetVolumeContext()
	maxShares, err := azureutils.GetMaxShares(params)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "MaxShares value not supported")
	}

	if !azureutils.IsValidVolumeCapabilities([]*csi.VolumeCapability{volumeCapability}, maxShares) {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not supported")
	}

	source := req.GetStagingTargetPath()
	if len(source) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	target := req.GetTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	err = preparePublishPath(target, d.mounter)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("Target path could not be prepared: %v", err))
	}

	mountOptions := []string{"bind"}
	if req.GetReadonly() {
		mountOptions = append(mountOptions, "ro")
	}

	switch req.GetVolumeCapability().GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		diskName, err := d.getVolumeName(volumeID)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "Unable to parse disk URI: %v", err)
		}
		source, err = d.evalSymLinks(filepath.Join("/dev/mapper", diskName))
		if err != nil {
			return nil, status.Errorf(codes.Internal, "NodePublishVolume: can not evaluate source path: %v", err)
		}

		klog.V(2).Infof("NodePublishVolume [block]: found device path %s", source)
		err = d.ensureBlockTargetFile(target)
		if err != nil {
			return nil, status.Errorf(codes.Internal, err.Error())
		}
	case *csi.VolumeCapability_Mount:
		mnt, err := d.ensureMountPoint(target)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "could not mount target %q: %v", target, err)
		}
		if mnt {
			klog.V(2).Infof("NodePublishVolume: already mounted on target %s", target)
			return &csi.NodePublishVolumeResponse{}, nil
		}
	}

	klog.V(2).Infof("NodePublishVolume: mounting %s at %s", source, target)
	if err := d.mounter.Mount(source, target, "", mountOptions); err != nil {
		return nil, status.Errorf(codes.Internal, "could not mount %q at %q: %v", source, target, err)
	}

	klog.V(2).Infof("NodePublishVolume: mount %s at %s successfully", source, target)

	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmount the volume from the target path
func (d *Driver) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	targetPath := req.GetTargetPath()
	volumeID := req.GetVolumeId()

	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in the request")
	}
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	klog.V(2).Infof("NodeUnpublishVolume: unmounting volume %s on %s", volumeID, targetPath)
	err := CleanupMountPoint(targetPath, d.mounter, true /*extensiveMountPointCheck*/)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount target %q: %v", targetPath, err)
	}

	klog.V(2).Infof("NodeUnpublishVolume: unmount volume %s on %s successfully", volumeID, targetPath)

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetCapabilities return the capabilities of the Node plugin
func (d *Driver) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: d.NSCap,
	}, nil
}

// NodeGetInfo return info of the node on which this plugin is running
func (d *Driver) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	topology := &csi.Topology{
		Segments: map[string]string{topologyKey: ""},
	}

	var failureDomainFromLabels, instanceTypeFromLabels string
	var err error

	if d.supportZone {
		var zone cloudprovider.Zone
		if d.getNodeInfoFromLabels {
			failureDomainFromLabels, instanceTypeFromLabels, err = getNodeInfoFromLabels(ctx, d.NodeID, d.cloud.KubeClient)
		} else {
			if runtime.GOOS == "windows" && (!d.cloud.UseInstanceMetadata || d.cloud.Metadata == nil) {
				zone, err = d.cloud.VMSet.GetZoneByNodeName(d.NodeID)
			} else {
				zone, err = d.cloud.GetZone(ctx)
			}
			if err != nil {
				klog.Warningf("get zone(%s) failed with: %v, fall back to get zone from node labels", d.NodeID, err)
				failureDomainFromLabels, instanceTypeFromLabels, err = getNodeInfoFromLabels(ctx, d.NodeID, d.cloud.KubeClient)
			}
		}
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("getNodeInfoFromLabels on node(%s) failed with %v", d.NodeID, err))
		}
		if zone.FailureDomain == "" {
			zone.FailureDomain = failureDomainFromLabels
		}

		klog.V(2).Infof("NodeGetInfo, nodeName: %s, failureDomain: %s", d.NodeID, zone.FailureDomain)
		if azureutils.IsValidAvailabilityZone(zone.FailureDomain, d.cloud.Location) {
			topology.Segments[topologyKey] = zone.FailureDomain
			topology.Segments[consts.WellKnownTopologyKey] = zone.FailureDomain
		}
	}

	maxDataDiskCount := d.VolumeAttachLimit
	if maxDataDiskCount < 0 {
		var instanceType string
		var err error
		if d.getNodeInfoFromLabels {
			if instanceTypeFromLabels == "" {
				_, instanceTypeFromLabels, err = getNodeInfoFromLabels(ctx, d.NodeID, d.cloud.KubeClient)
			}
		} else {
			if runtime.GOOS == "windows" && d.cloud.UseInstanceMetadata && d.cloud.Metadata != nil {
				var metadata *azure.InstanceMetadata
				metadata, err = d.cloud.Metadata.GetMetadata(azcache.CacheReadTypeDefault)
				if err == nil && metadata != nil && metadata.Compute != nil {
					instanceType = metadata.Compute.VMSize
					klog.V(2).Infof("NodeGetInfo: nodeName(%s), VM Size(%s)", d.NodeID, instanceType)
				}
			} else {
				instances, ok := d.cloud.Instances()
				if !ok {
					klog.Warningf("failed to get instances from cloud provider")
				} else {
					instanceType, err = instances.InstanceType(ctx, types.NodeName(d.NodeID))
				}
			}
			if err != nil {
				klog.Warningf("get instance type(%s) failed with: %v", d.NodeID, err)
			}
			if instanceType == "" && instanceTypeFromLabels == "" {
				klog.Warningf("fall back to get instance type from node labels")
				_, instanceTypeFromLabels, err = getNodeInfoFromLabels(ctx, d.NodeID, d.cloud.KubeClient)
			}
		}
		if err != nil {
			klog.Warningf("getNodeInfoFromLabels on node(%s) failed with %v", d.NodeID, err)
		}
		if instanceType == "" {
			instanceType = instanceTypeFromLabels
		}
		maxDataDiskCount = getMaxDataDiskCount(instanceType)
	}

	nodeID := d.NodeID
	if d.getNodeIDFromIMDS && d.cloud.UseInstanceMetadata && d.cloud.Metadata != nil {
		metadata, err := d.cloud.Metadata.GetMetadata(azcache.CacheReadTypeDefault)
		if err == nil && metadata != nil && metadata.Compute != nil {
			klog.V(2).Infof("NodeGetInfo: NodeID(%s), metadata.Compute.Name(%s)", d.NodeID, metadata.Compute.Name)
			if metadata.Compute.Name != "" {
				if metadata.Compute.VMScaleSetName != "" {
					id, err := getVMSSInstanceName(metadata.Compute.Name)
					if err != nil {
						klog.Errorf("getVMSSInstanceName failed with %v", err)
					} else {
						nodeID = id
					}
				} else {
					nodeID = metadata.Compute.Name
				}
			}
		} else {
			klog.Warningf("get instance type(%s) failed with: %v", d.NodeID, err)
		}
	}

	return &csi.NodeGetInfoResponse{
		NodeId:             nodeID,
		MaxVolumesPerNode:  maxDataDiskCount,
		AccessibleTopology: topology,
	}, nil
}

func getMaxDataDiskCount(instanceType string) int64 {
	vmsize := strings.ToUpper(instanceType)
	maxDataDiskCount, exists := maxDataDiskCountMap[vmsize]
	if exists {
		klog.V(5).Infof("got a matching size in getMaxDataDiskCount, VM Size: %s, MaxDataDiskCount: %d", vmsize, maxDataDiskCount)
		return maxDataDiskCount
	}

	klog.V(5).Infof("not found a matching size in getMaxDataDiskCount, VM Size: %s, use default volume limit: %d", vmsize, defaultAzureVolumeLimit)
	return defaultAzureVolumeLimit
}

func (d *Driver) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	if len(req.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume ID was empty")
	}
	if len(req.VolumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume path was empty")
	}

	volUsage, err := GetVolumeStats(ctx, d.mounter, req.VolumePath, d.hostUtil)
	return &csi.NodeGetVolumeStatsResponse{
		Usage: volUsage,
	}, err
}

// NodeExpandVolume node expand volume
func (d *Driver) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}
	diskName, err := d.getVolumeName(volumeID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Unable to parse disk URI: %v", err)
	}

	capacityBytes := req.GetCapacityRange().GetRequiredBytes()
	volSizeBytes := int64(capacityBytes - cryptmapper.LUKSHeaderSize) // LUKS2 header is 16MiB, subtract from request size to get expected value)
	requestGiB := volumehelper.RoundUpGiB(volSizeBytes)

	volumePath := req.GetVolumePath()
	if len(volumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume path must be provided")
	}

	isBlock, err := d.getHostUtil().PathIsDevice(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "failed to determine device path for volumePath [%v]: %v", volumePath, err)
	}
	if !isBlock {
		volumeCapability := req.GetVolumeCapability()
		if volumeCapability != nil {
			isBlock = volumeCapability.GetBlock() != nil
		}
	}

	// [Edgeless] need to acquire lock before resizing block device LUKS partition
	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	if isBlock {
		if d.enableDiskOnlineResize {
			klog.V(2).Info("NodeExpandVolume begin to rescan all devices on block volume(%s)", volumeID)
			if err := rescanAllVolumes(d.ioHandler); err != nil {
				klog.Errorf("NodeExpandVolume rescanAllVolumes failed with error: %v", err)
			}
		}
		// [Edgeless] Resize LUKS partition
		if _, err := d.cryptMapper.ResizeCryptDevice(ctx, diskName); err != nil {
			return nil, status.Errorf(codes.Internal, "resizing crypt device: %v", err)
		}
		klog.V(2).Info("NodeExpandVolume skip resize operation on block volume(%s)", volumeID)
		return &csi.NodeExpandVolumeResponse{}, nil
	}

	devicePath, err := d.cryptMapper.GetDevicePath(diskName)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	if d.enableDiskOnlineResize {
		klog.V(2).Infof("NodeExpandVolume begin to rescan device %s on volume(%s)", devicePath, volumeID)
		if err := rescanVolume(d.ioHandler, devicePath); err != nil {
			klog.Errorf("NodeExpandVolume rescanVolume failed with error: %v", err)
		}
	}

	// [Edgeless] Resize LUKS partition
	devicePath, err = d.cryptMapper.ResizeCryptDevice(ctx, diskName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resizing crypt device: %v", err)
	}

	var retErr error
	if err := resizeVolume(devicePath, volumePath, d.mounter); err != nil {
		retErr = status.Errorf(codes.Internal, "could not resize volume %q (%q):  %v", volumeID, devicePath, err)
		klog.Errorf("%v, will continue checking whether the volume has been resized", retErr)
	}

	gotBlockSizeBytes, err := getBlockSizeBytes(devicePath, d.mounter)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("could not get size of block volume at path %s: %v", devicePath, err))
	}
	gotBlockGiB := volumehelper.RoundUpGiB(gotBlockSizeBytes)
	if gotBlockGiB < requestGiB {
		if retErr != nil {
			return nil, retErr
		}
		// Because size was rounded up, getting more size than requested will be a success.
		return nil, status.Errorf(codes.Internal, "resize requested for %v, but after resizing volume size was %v", requestGiB, gotBlockGiB)
	}

	klog.V(2).Infof("NodeExpandVolume succeeded on resizing volume %v to %v", volumeID, gotBlockSizeBytes)

	return &csi.NodeExpandVolumeResponse{
		CapacityBytes: gotBlockSizeBytes,
	}, nil
}

// ensureMountPoint: create mount point if not exists
// return <true, nil> if it's already a mounted point otherwise return <false, nil>
func (d *Driver) ensureMountPoint(target string) (bool, error) {
	notMnt, err := d.mounter.IsLikelyNotMountPoint(target)
	if err != nil && !os.IsNotExist(err) {
		if azureutils.IsCorruptedDir(target) {
			notMnt = false
			klog.Warningf("detected corrupted mount for targetPath [%s]", target)
		} else {
			return !notMnt, err
		}
	}

	if runtime.GOOS != "windows" {
		// Check all the mountpoints in case IsLikelyNotMountPoint
		// cannot handle --bind mount
		mountList, err := d.mounter.List()
		if err != nil {
			return !notMnt, err
		}

		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return !notMnt, err
		}

		for _, mountPoint := range mountList {
			if mountPoint.Path == targetAbs {
				notMnt = false
				break
			}
		}
	}

	if !notMnt {
		// testing original mount point, make sure the mount link is valid
		_, err := os.ReadDir(target)
		if err == nil {
			klog.V(2).Infof("already mounted to target %s", target)
			return !notMnt, nil
		}
		// mount link is invalid, now unmount and remount later
		klog.Warningf("ReadDir %s failed with %v, unmount this directory", target, err)
		if err := d.mounter.Unmount(target); err != nil {
			klog.Errorf("Unmount directory %s failed with %v", target, err)
			return !notMnt, err
		}
		notMnt = true
		return !notMnt, err
	}

	if runtime.GOOS != "windows" {
		// in windows, we will use mklink to mount, will MkdirAll in Mount func
		if err := volumehelper.MakeDir(target); err != nil {
			klog.Errorf("mkdir failed on target: %s (%v)", target, err)
			return !notMnt, err
		}
	}

	return !notMnt, nil
}

func (d *Driver) formatAndMount(source, target, fstype string, options []string) error {
	return formatAndMount(source, target, fstype, options, d.mounter)
}

func (d *Driver) getDevicePathWithLUN(lunStr string) (string, error) {
	lun, err := azureutils.GetDiskLUN(lunStr)
	if err != nil {
		return "", err
	}

	scsiHostRescan(d.ioHandler, d.mounter)

	newDevicePath := ""
	err = wait.PollImmediate(1*time.Second, 2*time.Minute, func() (bool, error) {
		var err error
		if newDevicePath, err = findDiskByLun(int(lun), d.ioHandler, d.mounter); err != nil {
			return false, fmt.Errorf("azureDisk - findDiskByLun(%v) failed with error(%s)", lun, err)
		}

		// did we find it?
		if newDevicePath != "" {
			return true, nil
		}
		// wait until timeout
		return false, nil
	})
	if err == nil && newDevicePath == "" {
		err = fmt.Errorf("azureDisk - findDiskByLun(%v) failed within timeout", lun)
	}
	return newDevicePath, err
}

func (d *Driver) ensureBlockTargetFile(target string) error {
	// Since the block device target path is file, its parent directory should be ensured to be valid.
	parentDir := filepath.Dir(target)
	if _, err := d.ensureMountPoint(parentDir); err != nil {
		return status.Errorf(codes.Internal, "could not mount target %q: %v", parentDir, err)
	}
	// Create the mount point as a file since bind mount device node requires it to be a file
	klog.V(2).Infof("ensureBlockTargetFile [block]: making target file %s", target)
	err := volumehelper.MakeFile(target)
	if err != nil {
		if removeErr := os.Remove(target); removeErr != nil {
			return status.Errorf(codes.Internal, "could not remove mount target %q: %v", target, removeErr)
		}
		return status.Errorf(codes.Internal, "could not create file %q: %v", target, err)
	}

	return nil
}

func collectMountOptions(fsType string, mntFlags []string) []string {
	var options []string
	options = append(options, mntFlags...)

	// By default, xfs does not allow mounting of two volumes with the same filesystem uuid.
	// Force ignore this uuid to be able to mount volume + its clone / restored snapshot on the same node.
	if fsType == "xfs" {
		options = append(options, "nouuid")
	}
	return options
}
