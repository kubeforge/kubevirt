/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2018 Red Hat, Inc.
 *
 */

package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc"

	v1 "kubevirt.io/kubevirt/pkg/api/v1"
	vmSchema "kubevirt.io/kubevirt/pkg/api/v1"
	hooks "kubevirt.io/kubevirt/pkg/hooks"
	hooksInfo "kubevirt.io/kubevirt/pkg/hooks/info"
	hooksv1alpha2 "kubevirt.io/kubevirt/pkg/hooks/v1alpha2"
	"kubevirt.io/kubevirt/pkg/log"
	domainSchema "kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

const baseBoardManufacturerAnnotation = "smbios.vm.kubevirt.io/baseBoardManufacturer"

type infoServer struct{}

func (s infoServer) Info(ctx context.Context, params *hooksInfo.InfoParams) (*hooksInfo.InfoResult, error) {
	log.Log.Info("Hook's Info method has been called")

	return &hooksInfo.InfoResult{
		Name: "smbios",
		Versions: []string{
			hooksv1alpha2.Version,
		},
		HookPoints: []*hooksInfo.HookPoint{
			&hooksInfo.HookPoint{
				Name:     hooksInfo.OnDefineDomainHookPointName,
				Priority: 0,
			},
			&hooksInfo.HookPoint{
				Name:     hooksInfo.OnSyncVMIHookPointName,
				Priority: 1,
			},
			&hooksInfo.HookPoint{
				Name:     hooksInfo.PreCloudInitIsoHookPointName,
				Priority: 1,
			},
		},
	}, nil
}

type v1alpha2Server struct{}

var initalAnnotation = ""

func (s v1alpha2Server) OnDefineDomain(ctx context.Context, params *hooksv1alpha2.OnDefineDomainParams) (*hooksv1alpha2.OnDefineDomainResult, error) {
	log.Log.Info("Hook's OnDefineDomain callback method has been called")

	vmiJSON := params.GetVmi()
	vmiSpec := vmSchema.VirtualMachineInstance{}
	err := json.Unmarshal(vmiJSON, &vmiSpec)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to unmarshal given VMI spec: %s", vmiJSON)
		panic(err)
	}

	annotations := vmiSpec.GetAnnotations()

	if _, found := annotations[baseBoardManufacturerAnnotation]; !found {
		log.Log.Info("SM BIOS hook sidecar was requested, but no attributes provided. Returning original domain spec")
		return &hooksv1alpha2.OnDefineDomainResult{
			DomainXML: params.GetDomainXML(),
		}, nil
	}

	initalAnnotation = annotations[baseBoardManufacturerAnnotation]

	domainXML := params.GetDomainXML()
	domainSpec := domainSchema.DomainSpec{}
	err = xml.Unmarshal(domainXML, &domainSpec)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to unmarshal given domain spec: %s", domainXML)
		panic(err)
	}

	domainSpec.OS.SMBios = &domainSchema.SMBios{Mode: "sysinfo"}

	if domainSpec.SysInfo == nil {
		domainSpec.SysInfo = &domainSchema.SysInfo{}
	}
	domainSpec.SysInfo.Type = "smbios"
	if baseBoardManufacturer, found := annotations[baseBoardManufacturerAnnotation]; found {
		domainSpec.SysInfo.BaseBoard = append(domainSpec.SysInfo.BaseBoard, domainSchema.Entry{
			Name:  "manufacturer",
			Value: baseBoardManufacturer,
		})
	}

	newDomainXML, err := xml.Marshal(domainSpec)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to marshal updated domain spec: %+v", domainSpec)
		panic(err)
	}

	log.Log.Info("Successfully updated original domain spec with requested SMBIOS attributes")

	return &hooksv1alpha2.OnDefineDomainResult{
		DomainXML: newDomainXML,
	}, nil
}

func (s v1alpha2Server) OnSyncVMI(ctx context.Context, params *hooksv1alpha2.OnSyncVMIParams) (*hooksv1alpha2.OnSyncVMIResult, error) {
	log.Log.Warning("Hook's OnSyncVMI callback method has been called!!!11")
	vmiSpec := vmSchema.VirtualMachineInstance{}
	annotations := vmiSpec.GetAnnotations()

	if _, found := annotations[baseBoardManufacturerAnnotation]; !found {
		log.Log.Info("SM BIOS hook sidecar was requested, but no attributes provided. Returning original domain spec")
		return &hooksv1alpha2.OnSyncVMIResult{
			NewDomainXML: params.GetNewDomainXML(),
		}, nil
	}

	if annotations[baseBoardManufacturerAnnotation] != initalAnnotation {
		log.Log.Warning("Annotation changed - need to update XML!")
	}

	return &hooksv1alpha2.OnSyncVMIResult{
		NewDomainXML: params.GetNewDomainXML(),
	}, nil
}

func (s v1alpha2Server) PreCloudInitIso(ctx context.Context, params *hooksv1alpha2.PreCloudInitIsoParams) (*hooksv1alpha2.PreCloudInitIsoResult, error) {
	log.Log.Info("Hook's PreCloudInitIso callback method has been called")

	vmiJSON := params.GetVmi()
	vmi := v1.VirtualMachineInstance{}
	err := json.Unmarshal(vmiJSON, &vmi)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to unmarshal given VMI spec: %s", vmiJSON)
		panic(err)
	}

	cloudInitDataJSON := params.GetCloudInitData()
	cloudInitData := v1.CloudInitNoCloudSource{}
	err = json.Unmarshal(cloudInitDataJSON, &cloudInitData)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to unmarshal given CloudInitNoCloudSource: %s", cloudInitDataJSON)
		panic(err)
	}

	cloudInitData.UserData = "#cloud-config\n"
	cloudInitData.UserDataBase64 = ""

	response, err := json.Marshal(cloudInitData)
	if err != nil {
		return &hooksv1alpha2.PreCloudInitIsoResult{
			CloudInitData: params.GetCloudInitData(),
		}, fmt.Errorf("Failed to marshal CloudInitNoCloudSource: %v", cloudInitData)

	}

	return &hooksv1alpha2.PreCloudInitIsoResult{
		CloudInitData: response,
	}, nil
}

func main() {
	log.InitializeLogging("smbios-hook-sidecar")

	socketPath := hooks.HookSocketsSharedDirectory + "/smbios.sock"
	socket, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Log.Reason(err).Errorf("Failed to initialized socket on path: %s", socket)
		log.Log.Error("Check whether given directory exists and socket name is not already taken by other file")
		panic(err)
	}
	defer os.Remove(socketPath)

	server := grpc.NewServer([]grpc.ServerOption{}...)
	hooksInfo.RegisterInfoServer(server, infoServer{})
	hooksv1alpha2.RegisterCallbacksServer(server, v1alpha2Server{})
	log.Log.Infof("Starting hook server exposing 'info' and 'v1alpha2' services on socket %s", socketPath)
	server.Serve(socket)
}
