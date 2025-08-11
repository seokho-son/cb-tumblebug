/*
Copyright 2019 The Cloud-Barista Authors.
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

// Package mci is to manage multi-cloud infra
package infra

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloud-barista/cb-tumblebug/src/core/common"
	clientManager "github.com/cloud-barista/cb-tumblebug/src/core/common/client"
	"github.com/cloud-barista/cb-tumblebug/src/core/common/label"
	"github.com/cloud-barista/cb-tumblebug/src/core/model"
	"github.com/cloud-barista/cb-tumblebug/src/core/resource"
	"github.com/cloud-barista/cb-tumblebug/src/kvstore/kvstore"
	validator "github.com/go-playground/validator/v10"
	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
)

// TbMciReqStructLevelValidation is func to validate fields in TbMciReqStruct
func TbMciReqStructLevelValidation(sl validator.StructLevel) {

	u := sl.Current().Interface().(model.TbMciReq)

	err := common.CheckString(u.Name)
	if err != nil {
		// ReportError(field interface{}, fieldName, structFieldName, tag, param string)
		sl.ReportError(u.Name, "name", "Name", err.Error(), "")
	}
}

// TbVmReqStructLevelValidation is func to validate fields in model.TbVmReqStruct
func TbVmReqStructLevelValidation(sl validator.StructLevel) {

	u := sl.Current().Interface().(model.TbVmReq)

	err := common.CheckString(u.Name)
	if err != nil {
		// ReportError(field interface{}, fieldName, structFieldName, tag, param string)
		sl.ReportError(u.Name, "name", "Name", err.Error(), "")
	}
}

var holdingMciMap sync.Map

// createVmObjectSafe creates VM object without WaitGroup management
func createVmObjectSafe(nsId, mciId string, vmInfoData *model.TbVmInfo) error {
	// Check if VM object already exists (from CreateMciDynamic preparation)
	existingVm, err := GetVmObject(nsId, mciId, vmInfoData.Name)
	if err == nil {
		// VM object already exists, update it with new info instead of creating
		log.Debug().Msgf("VM object '%s' already exists, updating instead of creating", vmInfoData.Name)

		// Update the existing VM with the new configuration
		existingVm.Status = model.StatusCreating
		existingVm.TargetAction = model.ActionCreate
		existingVm.TargetStatus = model.StatusRunning
		existingVm.SpecId = vmInfoData.SpecId
		existingVm.ImageId = vmInfoData.ImageId
		existingVm.VNetId = vmInfoData.VNetId
		existingVm.SubnetId = vmInfoData.SubnetId
		existingVm.SecurityGroupIds = vmInfoData.SecurityGroupIds
		existingVm.SshKeyId = vmInfoData.SshKeyId
		existingVm.VmUserName = vmInfoData.VmUserName
		existingVm.VmUserPassword = vmInfoData.VmUserPassword
		existingVm.Description = vmInfoData.Description
		existingVm.RootDiskType = vmInfoData.RootDiskType
		existingVm.RootDiskSize = vmInfoData.RootDiskSize
		existingVm.DataDiskIds = vmInfoData.DataDiskIds

		// Update the VM object in kvstore
		UpdateVmInfo(nsId, mciId, existingVm)
		return nil
	}

	// VM object doesn't exist, create it normally
	var wg sync.WaitGroup
	wg.Add(1)
	return CreateVmObject(&wg, nsId, mciId, vmInfoData)
}

// createVmSafe creates VM without WaitGroup management
func createVmSafe(nsId, mciId string, vmInfoData *model.TbVmInfo, option string) error {
	var wg sync.WaitGroup
	wg.Add(1)
	err := CreateVm(&wg, nsId, mciId, vmInfoData, option)
	wg.Wait()
	return err
}

// Helper functions for CreateMci

// contains checks if a string slice contains a specific string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// createSubGroup creates a subGroup with proper error handling
func createSubGroup(nsId, mciId string, vmRequest *model.TbVmReq, subGroupSize, vmStartIndex int, uid string, req *model.TbMciReq) error {
	log.Info().Msgf("Creating MCI subGroup object for '%s'", vmRequest.Name)
	key := common.GenMciSubGroupKey(nsId, mciId, vmRequest.Name)

	subGroupInfoData := model.TbSubGroupInfo{
		ResourceType: model.StrSubGroup,
		Id:           common.ToLower(vmRequest.Name),
		Name:         common.ToLower(vmRequest.Name),
		Uid:          common.GenUid(),
		SubGroupSize: vmRequest.SubGroupSize,
	}

	// Build VM ID list
	for i := vmStartIndex; i < subGroupSize+vmStartIndex; i++ {
		subGroupInfoData.VmId = append(subGroupInfoData.VmId, subGroupInfoData.Id+"-"+strconv.Itoa(i))
	}

	// Marshal with error handling
	val, err := json.Marshal(subGroupInfoData)
	if err != nil {
		return fmt.Errorf("failed to marshal subGroup data: %w", err)
	}

	if err := kvstore.Put(key, string(val)); err != nil {
		return fmt.Errorf("failed to store subGroup data: %w", err)
	}

	// Store label info
	labels := map[string]string{
		model.LabelManager:        model.StrManager,
		model.LabelNamespace:      nsId,
		model.LabelLabelType:      model.StrSubGroup,
		model.LabelId:             subGroupInfoData.Id,
		model.LabelName:           subGroupInfoData.Name,
		model.LabelUid:            subGroupInfoData.Uid,
		model.LabelMciId:          mciId,
		model.LabelMciName:        req.Name,
		model.LabelMciUid:         uid,
		model.LabelMciDescription: req.Description,
	}

	return label.CreateOrUpdateLabel(model.StrSubGroup, uid, key, labels)
}

// createMciObject creates the MCI object with proper error handling
func createMciObject(nsId, mciId string, req *model.TbMciReq, uid string) error {
	log.Info().Msg("Creating MCI object")
	key := common.GenMciKey(nsId, mciId, "")

	mciInfo := model.TbMciInfo{
		ResourceType:    model.StrMCI,
		Id:              mciId,
		Name:            req.Name,
		Uid:             uid,
		Description:     req.Description,
		Status:          model.StatusCreating,
		TargetAction:    model.ActionCreate,
		TargetStatus:    model.StatusRunning,
		InstallMonAgent: req.InstallMonAgent,
		SystemLabel:     req.SystemLabel,
		PostCommand:     req.PostCommand,
	}

	val, err := json.Marshal(mciInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal MCI info: %w", err)
	}

	if err := kvstore.Put(key, string(val)); err != nil {
		return fmt.Errorf("failed to store MCI object: %w", err)
	}

	// Store label info
	labels := map[string]string{
		model.LabelManager:     model.StrManager,
		model.LabelNamespace:   nsId,
		model.LabelLabelType:   model.StrMCI,
		model.LabelId:          mciId,
		model.LabelName:        req.Name,
		model.LabelUid:         uid,
		model.LabelDescription: req.Description,
	}
	for key, value := range req.Label {
		labels[key] = value
	}

	return label.CreateOrUpdateLabel(model.StrMCI, uid, key, labels)
}

// handleHoldOption handles the hold option logic
func handleHoldOption(nsId, mciId string) error {
	key := common.GenMciKey(nsId, mciId, "")
	holdingMciMap.Store(key, "holding")

	for {
		value, ok := holdingMciMap.Load(key)
		if !ok {
			break
		}
		if value == "continue" {
			holdingMciMap.Delete(key)
			break
		} else if value == "withdraw" {
			holdingMciMap.Delete(key)
			DelMci(nsId, mciId, "force")
			return fmt.Errorf("MCI creation was withdrawn by user")
		}

		log.Info().Msgf("MCI: %s (holding)", key)
		time.Sleep(5 * time.Second)
	}

	return nil
}

// cleanupPartialMci cleans up partially created MCI resources
func cleanupPartialMci(nsId, mciId string) error {
	log.Warn().Msgf("Cleaning up partial MCI: %s/%s", nsId, mciId)

	// Attempt to delete MCI - this will handle cleanup of VMs and other resources
	_, err := DelMci(nsId, mciId, "force")
	if err != nil {
		return fmt.Errorf("failed to cleanup partial MCI: %w", err)
	}

	return nil
}

// handleMonitoringAgent handles CB-Dragonfly monitoring agent installation
func handleMonitoringAgent(nsId, mciId string, mciTmp model.TbMciInfo, option string) error {
	if !strings.Contains(mciTmp.InstallMonAgent, "yes") || option == "register" {
		return nil
	}

	log.Info().Msg("Installing CB-Dragonfly monitoring agent")

	if err := CheckDragonflyEndpoint(); err != nil {
		log.Warn().Msg("CB-Dragonfly is not available, skipping agent installation")
		return nil
	}

	reqToMon := &model.MciCmdReq{
		UserName: "cb-user", // TODO: Make this configurable
	}

	// Intelligent wait time based on VM count
	waitTime := 30 * time.Second
	if len(mciTmp.Vm) > 5 {
		waitTime = 60 * time.Second
	}

	log.Info().Msgf("Waiting %v for safe CB-Dragonfly Agent installation", waitTime)
	time.Sleep(waitTime)

	content, err := InstallMonitorAgentToMci(nsId, mciId, model.StrMCI, reqToMon)
	if err != nil {
		return fmt.Errorf("failed to install monitoring agent: %w", err)
	}

	log.Info().Msg("CB-Dragonfly monitoring agent installed successfully")
	common.PrintJsonPretty(content)
	return nil
}

// handlePostCommands handles post-deployment command execution
func handlePostCommands(nsId, mciId string, mciTmp model.TbMciInfo) error {
	if len(mciTmp.PostCommand.Command) == 0 {
		return nil
	}

	log.Info().Msg("Executing post-deployment commands")
	log.Info().Msgf("Waiting 5 seconds for safe bootstrapping")
	time.Sleep(5 * time.Second)

	log.Info().Msgf("Executing commands: %+v", mciTmp.PostCommand)
	output, err := RemoteCommandToMci(nsId, mciId, "", "", "", &mciTmp.PostCommand)
	if err != nil {
		return fmt.Errorf("failed to execute post-deployment commands: %w", err)
	}

	result := model.MciSshCmdResult{
		Results: output,
	}

	common.PrintJsonPretty(result)
	mciTmp.PostCommandResult = result
	UpdateMciInfo(nsId, mciTmp)

	log.Info().Msg("Post-deployment commands executed successfully")
	return nil
}

// CreatedResource represents a resource created during dynamic MCI provisioning
type CreatedResource struct {
	Type string `json:"type"` // "vnet", "sshkey", "securitygroup"
	Id   string `json:"id"`   // Resource ID
}

// VmReqWithCreatedResources contains VM request and list of created resources for rollback
type VmReqWithCreatedResources struct {
	VmReq            *model.TbVmReq    `json:"vmReq"`
	CreatedResources []CreatedResource `json:"createdResources"`
}

// rollbackCreatedResources deletes only the resources that were created during this MCI creation
func rollbackCreatedResources(nsId string, createdResources []CreatedResource) error {
	var errors []string
	var successes []string

	vNetIds := make([]string, 0)
	sshKeyIds := make([]string, 0)
	securityGroupIds := make([]string, 0)

	log.Info().Msgf("Starting rollback process for %d resources in namespace '%s'", len(createdResources), nsId)

	// Group resources by type for logging
	for _, res := range createdResources {
		switch res.Type {
		case model.StrVNet:
			vNetIds = append(vNetIds, res.Id)
		case model.StrSSHKey:
			sshKeyIds = append(sshKeyIds, res.Id)
		case model.StrSecurityGroup:
			securityGroupIds = append(securityGroupIds, res.Id)
		}
	}

	log.Info().Msgf("Resources to rollback: VNet(%d): %v, SSHKey(%d): %v, SecurityGroup(%d): %v",
		len(vNetIds), vNetIds, len(sshKeyIds), sshKeyIds, len(securityGroupIds), securityGroupIds)

	// Use semaphore for parallel processing with concurrency limit
	const maxConcurrency = 10
	semaphore := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	var mutex sync.Mutex

	// Delete SSHKeys first (usually least dependent) in parallel
	for _, res := range sshKeyIds {
		wg.Add(1)
		go func(resourceId string) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }() // Release semaphore

			if err := resource.DelResource(nsId, model.StrSSHKey, resourceId, "false"); err != nil {
				errorMsg := fmt.Sprintf("Failed to delete SSHKey '%s' in namespace '%s': %v", resourceId, nsId, err)
				mutex.Lock()
				errors = append(errors, errorMsg)
				mutex.Unlock()
				log.Error().Err(err).Msgf("Rollback failed for SSHKey: %s", resourceId)
			} else {
				successMsg := fmt.Sprintf("SSHKey '%s'", resourceId)
				mutex.Lock()
				successes = append(successes, successMsg)
				mutex.Unlock()
				log.Info().Msgf("Successfully rolled back SSHKey: %s", resourceId)
			}
		}(res)
	}

	// Wait for all SSHKey deletions to complete
	wg.Wait()
	log.Info().Msgf("Completed SSHKey deletions: %d successful, %d failed", len(sshKeyIds), len(errors))

	// Delete SecurityGroups second in parallel
	for _, res := range securityGroupIds {
		wg.Add(1)
		go func(resourceId string) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }() // Release semaphore

			if err := resource.DelResource(nsId, model.StrSecurityGroup, resourceId, "false"); err != nil {
				errorMsg := fmt.Sprintf("Failed to delete SecurityGroup '%s' in namespace '%s': %v", resourceId, nsId, err)
				mutex.Lock()
				errors = append(errors, errorMsg)
				mutex.Unlock()
				log.Error().Err(err).Msgf("Rollback failed for SecurityGroup: %s", resourceId)
			} else {
				successMsg := fmt.Sprintf("SecurityGroup '%s'", resourceId)
				mutex.Lock()
				successes = append(successes, successMsg)
				mutex.Unlock()
				log.Info().Msgf("Successfully rolled back SecurityGroup: %s", resourceId)
			}
		}(res)
	}

	// Wait for all SecurityGroup deletions to complete
	wg.Wait()
	log.Info().Msgf("Completed SecurityGroup deletions: %d total attempted", len(securityGroupIds))

	// wait for 5 secs for safe rollback
	time.Sleep(5 * time.Second)

	// Delete VNets last (most dependent) in parallel
	for _, res := range vNetIds {
		wg.Add(1)
		go func(resourceId string) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }() // Release semaphore

			if err := resource.DelResource(nsId, model.StrVNet, resourceId, "false"); err != nil {
				errorMsg := fmt.Sprintf("Failed to delete VNet '%s' in namespace '%s': %v", resourceId, nsId, err)
				mutex.Lock()
				errors = append(errors, errorMsg)
				mutex.Unlock()
				log.Error().Err(err).Msgf("Rollback failed for VNet: %s", resourceId)
			} else {
				successMsg := fmt.Sprintf("VNet '%s'", resourceId)
				mutex.Lock()
				successes = append(successes, successMsg)
				mutex.Unlock()
				log.Info().Msgf("Successfully rolled back VNet: %s", resourceId)
			}
		}(res)
	}

	// Wait for all VNet deletions to complete
	wg.Wait()
	log.Info().Msgf("Completed VNet deletions: %d total attempted", len(vNetIds))

	// Log rollback summary
	log.Info().Msgf("Rollback summary: Success(%d): %v, Failed(%d): %d errors",
		len(successes), successes, len(errors), len(errors))

	if len(errors) > 0 {
		return fmt.Errorf("rollback completed with %d errors: %s", len(errors), strings.Join(errors, "; "))
	}

	log.Info().Msgf("All %d resources successfully rolled back in namespace '%s'", len(createdResources), nsId)
	return nil
}

// MCI and VM Provisioning

// CreateMciVm is func to post (create) MciVm
func CreateMciVm(nsId string, mciId string, vmInfoData *model.TbVmInfo) (*model.TbVmInfo, error) {

	err := common.CheckString(nsId)
	if err != nil {
		temp := &model.TbVmInfo{}
		log.Error().Err(err).Msg("")
		return temp, err
	}

	err = common.CheckString(mciId)
	if err != nil {
		temp := &model.TbVmInfo{}
		log.Error().Err(err).Msg("")
		return temp, err
	}
	err = common.CheckString(vmInfoData.Name)
	if err != nil {
		temp := &model.TbVmInfo{}
		log.Error().Err(err).Msg("")
		return temp, err
	}
	check, _ := CheckVm(nsId, mciId, vmInfoData.Name)

	if check {
		temp := &model.TbVmInfo{}
		err := fmt.Errorf("The vm " + vmInfoData.Name + " already exists.")
		return temp, err
	}

	vmInfoData.Id = vmInfoData.Name
	vmInfoData.PublicIP = "empty"
	vmInfoData.PublicDNS = "empty"
	vmInfoData.TargetAction = model.ActionCreate
	vmInfoData.TargetStatus = model.StatusRunning
	vmInfoData.Status = model.StatusCreating

	//goroutin
	var wg sync.WaitGroup
	wg.Add(1)
	option := "create"
	go CreateVmObject(&wg, nsId, mciId, vmInfoData)
	wg.Wait()

	wg.Add(1)
	go CreateVm(&wg, nsId, mciId, vmInfoData, option)
	wg.Wait()

	vmStatus, err := FetchVmStatus(nsId, mciId, vmInfoData.Id)
	if err != nil {
		return nil, fmt.Errorf("Cannot find " + common.GenMciKey(nsId, mciId, vmInfoData.Id))
	}

	vmInfoData.Status = vmStatus.Status
	vmInfoData.TargetStatus = vmStatus.TargetStatus
	vmInfoData.TargetAction = vmStatus.TargetAction

	// Install CB-Dragonfly monitoring agent

	mciTmp, _ := GetMciObject(nsId, mciId)

	fmt.Printf("\n[Init monitoring agent] for %+v\n - req.InstallMonAgent: %+v\n\n", mciId, mciTmp.InstallMonAgent)

	if strings.Contains(mciTmp.InstallMonAgent, "yes") {

		// Sleep for 20 seconds for a safe DF agent installation.
		fmt.Printf("\n\n[Info] Sleep for 20 seconds for safe CB-Dragonfly Agent installation.\n\n")
		time.Sleep(20 * time.Second)

		check := CheckDragonflyEndpoint()
		if check != nil {
			fmt.Printf("\n\n[Warning] CB-Dragonfly is not available\n\n")
		} else {
			reqToMon := &model.MciCmdReq{}
			reqToMon.UserName = "cb-user" // this MCI user name is temporal code. Need to improve.

			fmt.Printf("\n[InstallMonitorAgentToMci]\n\n")
			content, err := InstallMonitorAgentToMci(nsId, mciId, model.StrMCI, reqToMon)
			if err != nil {
				log.Error().Err(err).Msg("")
				//mciTmp.InstallMonAgent = "no"
			}
			common.PrintJsonPretty(content)
			//mciTmp.InstallMonAgent = "yes"
		}
	}

	return vmInfoData, nil
}

// ScaleOutMciSubGroup is func to create MCI groupVM
func ScaleOutMciSubGroup(nsId string, mciId string, subGroupId string, numVMsToAdd string) (*model.TbMciInfo, error) {
	vmIdList, err := ListVmBySubGroup(nsId, mciId, subGroupId)
	if err != nil {
		temp := &model.TbMciInfo{}
		return temp, err
	}
	vmObj, err := GetVmObject(nsId, mciId, vmIdList[0])

	vmTemplate := &model.TbVmReq{}

	// only take template required to create VM
	vmTemplate.Name = vmObj.SubGroupId
	vmTemplate.ConnectionName = vmObj.ConnectionName
	vmTemplate.ImageId = vmObj.ImageId
	vmTemplate.SpecId = vmObj.SpecId
	vmTemplate.VNetId = vmObj.VNetId
	vmTemplate.SubnetId = vmObj.SubnetId
	vmTemplate.SecurityGroupIds = vmObj.SecurityGroupIds
	vmTemplate.SshKeyId = vmObj.SshKeyId
	vmTemplate.VmUserName = vmObj.VmUserName
	vmTemplate.VmUserPassword = vmObj.VmUserPassword
	vmTemplate.RootDiskType = vmObj.RootDiskType
	vmTemplate.RootDiskSize = vmObj.RootDiskSize
	vmTemplate.Description = vmObj.Description

	vmTemplate.SubGroupSize = numVMsToAdd

	result, err := CreateMciGroupVm(nsId, mciId, vmTemplate, true)
	if err != nil {
		temp := &model.TbMciInfo{}
		return temp, err
	}
	return result, nil

}

// CreateMciGroupVm is func to create MCI groupVM
func CreateMciGroupVm(nsId string, mciId string, vmRequest *model.TbVmReq, newSubGroup bool) (*model.TbMciInfo, error) {

	err := common.CheckString(nsId)
	if err != nil {
		temp := &model.TbMciInfo{}
		log.Error().Err(err).Msg("")
		return temp, err
	}

	err = common.CheckString(mciId)
	if err != nil {
		temp := &model.TbMciInfo{}
		log.Error().Err(err).Msg("")
		return temp, err
	}

	// returns InvalidValidationError for bad validation input, nil or ValidationErrors ( []FieldError )
	err = validate.Struct(vmRequest)
	if err != nil {

		// this check is only needed when your code could produce
		// an invalid value for validation such as interface with nil
		// value most including myself do not usually have code like this.
		if _, ok := err.(*validator.InvalidValidationError); ok {
			log.Err(err).Msg("")
			return nil, err
		}

		// for _, err := range err.(validator.ValidationErrors) {

		// 	fmt.Println(err.Namespace()) // can differ when a custom TagNameFunc is registered or
		// 	fmt.Println(err.Field())     // by passing alt name to ReportError like below
		// 	fmt.Println(err.StructNamespace())
		// 	fmt.Println(err.StructField())
		// 	fmt.Println(err.Tag())
		// 	fmt.Println(err.ActualTag())
		// 	fmt.Println(err.Kind())
		// 	fmt.Println(err.Type())
		// 	fmt.Println(err.Value())
		// 	fmt.Println(err.Param())
		// 	fmt.Println()
		// }

		return nil, err
	}

	mciTmp, err := GetMciObject(nsId, mciId)

	if err != nil {
		temp := &model.TbMciInfo{}
		return temp, err
	}

	//vmRequest := req

	targetAction := model.ActionCreate
	targetStatus := model.StatusRunning

	//goroutin
	var wg sync.WaitGroup

	// subGroup handling
	subGroupSize, err := strconv.Atoi(vmRequest.SubGroupSize)
	fmt.Printf("subGroupSize: %v\n", subGroupSize)

	// make subGroup default (any VM going to be in a subGroup)
	if subGroupSize < 1 || err != nil {
		subGroupSize = 1
	}

	vmStartIndex := 1

	tentativeVmId := common.ToLower(vmRequest.Name)

	err = common.CheckString(tentativeVmId)
	if err != nil {
		log.Error().Err(err).Msg("")
		return &model.TbMciInfo{}, err
	}

	if subGroupSize > 0 {

		log.Info().Msg("Create MCI subGroup object")

		subGroupInfoData := model.TbSubGroupInfo{}
		subGroupInfoData.ResourceType = model.StrSubGroup
		subGroupInfoData.Id = tentativeVmId
		subGroupInfoData.Name = tentativeVmId
		subGroupInfoData.Uid = common.GenUid()
		subGroupInfoData.SubGroupSize = vmRequest.SubGroupSize

		key := common.GenMciSubGroupKey(nsId, mciId, vmRequest.Name)
		keyValue, err := kvstore.GetKv(key)
		if err != nil {
			err = fmt.Errorf("In CreateMciGroupVm(); kvstore.GetKv(): " + err.Error())
			log.Error().Err(err).Msg("")
		}
		if keyValue != (kvstore.KeyValue{}) {
			if newSubGroup {
				json.Unmarshal([]byte(keyValue.Value), &subGroupInfoData)
				existingVmSize, err := strconv.Atoi(subGroupInfoData.SubGroupSize)
				if err != nil {
					err = fmt.Errorf("In CreateMciGroupVm(); kvstore.GetKv(): " + err.Error())
					log.Error().Err(err).Msg("")
				}
				// add the number of existing VMs in the SubGroup with requested number for additions
				subGroupInfoData.SubGroupSize = strconv.Itoa(existingVmSize + subGroupSize)
				vmStartIndex = existingVmSize + 1
			} else {
				err = fmt.Errorf("Duplicated SubGroup ID")
				log.Error().Err(err).Msg("")
				return nil, err
			}
		}

		for i := vmStartIndex; i < subGroupSize+vmStartIndex; i++ {
			subGroupInfoData.VmId = append(subGroupInfoData.VmId, subGroupInfoData.Id+"-"+strconv.Itoa(i))
		}

		val, _ := json.Marshal(subGroupInfoData)
		err = kvstore.Put(key, string(val))
		if err != nil {
			log.Error().Err(err).Msg("")
		}
		// check stored subGroup object
		keyValue, err = kvstore.GetKv(key)
		if err != nil {
			err = fmt.Errorf("In CreateMciGroupVm(); kvstore.GetKv(): " + err.Error())
			log.Error().Err(err).Msg("")
			// return nil, err
		}

	}

	for i := vmStartIndex; i < subGroupSize+vmStartIndex; i++ {
		vmInfoData := model.TbVmInfo{}

		if subGroupSize == 0 { // for VM (not in a group)
			vmInfoData.Name = common.ToLower(vmRequest.Name)
		} else { // for VM (in a group)
			vmInfoData.SubGroupId = common.ToLower(vmRequest.Name)
			vmInfoData.Name = common.ToLower(vmRequest.Name) + "-" + strconv.Itoa(i)

			log.Debug().Msg("vmInfoData.Name: " + vmInfoData.Name)

		}
		vmInfoData.ResourceType = model.StrVM
		vmInfoData.Id = vmInfoData.Name
		vmInfoData.Uid = common.GenUid()

		vmInfoData.PublicIP = "empty"
		vmInfoData.PublicDNS = "empty"

		vmInfoData.Status = model.StatusCreating
		vmInfoData.TargetAction = targetAction
		vmInfoData.TargetStatus = targetStatus

		vmInfoData.ConnectionName = vmRequest.ConnectionName
		vmInfoData.ConnectionConfig, err = common.GetConnConfig(vmRequest.ConnectionName)
		if err != nil {
			err = fmt.Errorf("Cannot retrieve ConnectionConfig" + err.Error())
			log.Error().Err(err).Msg("")
		}
		vmInfoData.Location = vmInfoData.ConnectionConfig.RegionDetail.Location
		vmInfoData.SpecId = vmRequest.SpecId
		vmInfoData.ImageId = vmRequest.ImageId
		vmInfoData.VNetId = vmRequest.VNetId
		vmInfoData.SubnetId = vmRequest.SubnetId
		vmInfoData.SecurityGroupIds = vmRequest.SecurityGroupIds
		vmInfoData.DataDiskIds = vmRequest.DataDiskIds
		vmInfoData.SshKeyId = vmRequest.SshKeyId
		vmInfoData.Description = vmRequest.Description
		vmInfoData.VmUserName = vmRequest.VmUserName
		vmInfoData.VmUserPassword = vmRequest.VmUserPassword
		vmInfoData.RootDiskType = vmRequest.RootDiskType
		vmInfoData.RootDiskSize = vmRequest.RootDiskSize

		vmInfoData.Label = vmRequest.Label

		vmInfoData.CspResourceId = vmRequest.CspResourceId

		wg.Add(1)
		go CreateVmObject(&wg, nsId, mciId, &vmInfoData)
	}
	wg.Wait()

	option := "create"

	for i := vmStartIndex; i < subGroupSize+vmStartIndex; i++ {
		vmInfoData := model.TbVmInfo{}

		if subGroupSize == 0 { // for VM (not in a group)
			vmInfoData.Name = common.ToLower(vmRequest.Name)
		} else { // for VM (in a group)
			vmInfoData.SubGroupId = common.ToLower(vmRequest.Name)
			vmInfoData.Name = common.ToLower(vmRequest.Name) + "-" + strconv.Itoa(i)
		}
		vmInfoData.Id = vmInfoData.Name
		vmId := vmInfoData.Id
		vmInfoData, err := GetVmObject(nsId, mciId, vmId)
		if err != nil {
			log.Error().Err(err).Msg("")
			return nil, err
		}

		// Avoid concurrent requests to CSP.
		time.Sleep(time.Millisecond * 1000)

		wg.Add(1)
		go CreateVm(&wg, nsId, mciId, &vmInfoData, option)
	}
	wg.Wait()

	//Update MCI status

	mciTmp, err = GetMciObject(nsId, mciId)
	if err != nil {
		temp := &model.TbMciInfo{}
		return temp, err
	}

	mciStatusTmp, _ := GetMciStatus(nsId, mciId)

	mciTmp.Status = mciStatusTmp.Status

	if mciTmp.TargetStatus == mciTmp.Status {
		mciTmp.TargetStatus = model.StatusComplete
		mciTmp.TargetAction = model.ActionComplete
	}
	UpdateMciInfo(nsId, mciTmp)

	// Install CB-Dragonfly monitoring agent

	if strings.Contains(mciTmp.InstallMonAgent, "yes") {

		// Sleep for 60 seconds for a safe DF agent installation.
		fmt.Printf("\n\n[Info] Sleep for 60 seconds for safe CB-Dragonfly Agent installation.\n\n")
		time.Sleep(60 * time.Second)

		check := CheckDragonflyEndpoint()
		if check != nil {
			fmt.Printf("\n\n[Warning] CB-Dragonfly is not available\n\n")
		} else {
			reqToMon := &model.MciCmdReq{}
			reqToMon.UserName = "cb-user" // this MCI user name is temporal code. Need to improve.

			fmt.Printf("\n[InstallMonitorAgentToMci]\n\n")
			content, err := InstallMonitorAgentToMci(nsId, mciId, model.StrMCI, reqToMon)
			if err != nil {
				log.Error().Err(err).Msg("")
				//mciTmp.InstallMonAgent = "no"
			}
			common.PrintJsonPretty(content)
			//mciTmp.InstallMonAgent = "yes"
		}
	}

	vmList, err := ListVmBySubGroup(nsId, mciId, tentativeVmId)

	if err != nil {
		mciTmp.SystemMessage = err.Error()
	}
	if vmList != nil {
		mciTmp.NewVmList = vmList
	}

	return &mciTmp, nil

}

// CreateMci is func to create MCI object and deploy requested VMs (register CSP native VM with option=register)
func CreateMci(nsId string, req *model.TbMciReq, option string) (*model.TbMciInfo, error) {
	// Input validation
	if err := common.CheckString(nsId); err != nil {
		log.Error().Err(err).Msg("Invalid namespace ID")
		return &model.TbMciInfo{}, fmt.Errorf("invalid namespace ID: %w", err)
	}

	if err := validate.Struct(req); err != nil {
		if _, ok := err.(*validator.InvalidValidationError); ok {
			log.Error().Err(err).Msg("Invalid validation error")
			return nil, fmt.Errorf("validation failed: %w", err)
		}
		log.Error().Err(err).Msg("Request validation failed")
		return nil, fmt.Errorf("request validation failed: %w", err)
	}

	// Initialize failure tracking
	var (
		vmObjectErrors []model.VmCreationError
		vmCreateErrors []model.VmCreationError
		totalVmCount   int
		errorMu        sync.Mutex
	)

	// Count total VMs to be created
	for _, vmReq := range req.Vm {
		if vmReq.SubGroupSize != "" {
			if size, err := strconv.Atoi(vmReq.SubGroupSize); err == nil && size > 0 {
				totalVmCount += size
			} else {
				totalVmCount += 1
			}
		} else {
			totalVmCount += 1
		}
	}

	// Helper function to add VM creation error (mutex-free version for when already locked)
	addVmErrorUnsafe := func(errors *[]model.VmCreationError, vmName, errorMsg, phase string) {
		*errors = append(*errors, model.VmCreationError{
			VmName:    vmName,
			Error:     errorMsg,
			Phase:     phase,
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}

	// Helper function to add VM creation error (with mutex for standalone use)
	addVmError := func(errors *[]model.VmCreationError, vmName, errorMsg, phase string) {
		errorMu.Lock()
		defer errorMu.Unlock()
		*errors = append(*errors, model.VmCreationError{
			VmName:    vmName,
			Error:     errorMsg,
			Phase:     phase,
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}

	// Check MCI existence
	var existingMci *model.TbMciInfo
	var mciAlreadyExists bool

	if option != "register" {
		if exists, _ := CheckMci(nsId, req.Name); exists {
			// Check if this is a prepared MCI from CreateMciDynamic
			mciObj, err := GetMciObject(nsId, req.Name)
			if err != nil {
				return nil, fmt.Errorf("failed to get existing MCI '%s': %w", req.Name, err)
			}

			// Allow continuing if MCI is in prepared state (from CreateMciDynamic)
			if mciObj.Status == model.StatusPrepared || mciObj.Status == model.StatusPreparing {
				existingMci = &mciObj
				mciAlreadyExists = true
				log.Info().Msgf("Found prepared MCI '%s', continuing with VM creation", req.Name)
			} else {
				return nil, fmt.Errorf("MCI '%s' already exists in namespace '%s' with status '%s'", req.Name, nsId, mciObj.Status)
			}
		}
	} else {
		req.SystemLabel = "Registered from CSP resource"
	}

	// Early validation of VM requests
	if len(req.Vm) == 0 {
		return nil, fmt.Errorf("no VM requests provided")
	}

	for i, vmReq := range req.Vm {
		if err := common.CheckString(vmReq.Name); err != nil {
			return nil, fmt.Errorf("invalid VM name at index %d: %w", i, err)
		}

		// Validate connection config early
		if _, err := common.GetConnConfig(vmReq.ConnectionName); err != nil {
			return nil, fmt.Errorf("invalid connection config '%s' for VM '%s': %w",
				vmReq.ConnectionName, vmReq.Name, err)
		}
	}

	// Initialize MCI
	uid := common.GenUid()
	mciId := req.Name

	// Pre-calculate VM configurations to avoid duplication
	type vmConfig struct {
		vmInfo       model.TbVmInfo
		subGroupSize int
		vmIndex      int
	}

	var vmConfigs []vmConfig
	var subGroupsCreated []string
	vmStartIndex := 1

	// Process VM requests and build configurations
	for _, vmRequest := range req.Vm {
		subGroupSize, err := strconv.Atoi(vmRequest.SubGroupSize)
		if err != nil {
			subGroupSize = 1
		}

		log.Debug().Msgf("Processing VM request '%s' with subGroupSize: %d", vmRequest.Name, subGroupSize)

		// Get connection config once and validate
		connectionConfig, err := common.GetConnConfig(vmRequest.ConnectionName)
		if err != nil {
			return nil, fmt.Errorf("cannot retrieve connection config for VM '%s': %w", vmRequest.Name, err)
		}

		// Create subGroup if needed
		if subGroupSize > 0 {
			subGroupName := common.ToLower(vmRequest.Name)
			if !contains(subGroupsCreated, subGroupName) {
				if err := createSubGroup(nsId, mciId, &vmRequest, subGroupSize, vmStartIndex, uid, req); err != nil {
					return nil, fmt.Errorf("failed to create subGroup '%s': %w", subGroupName, err)
				}
				subGroupsCreated = append(subGroupsCreated, subGroupName)
			}
		}

		// Build VM configurations
		for i := vmStartIndex; i < subGroupSize+vmStartIndex; i++ {
			vmInfo := model.TbVmInfo{
				ResourceType:     model.StrVM,
				Uid:              common.GenUid(),
				PublicIP:         "empty",
				PublicDNS:        "empty",
				Status:           model.StatusCreating,
				TargetAction:     model.ActionCreate,
				TargetStatus:     model.StatusRunning,
				ConnectionName:   vmRequest.ConnectionName,
				ConnectionConfig: connectionConfig,
				Location:         connectionConfig.RegionDetail.Location,
				SpecId:           vmRequest.SpecId,
				ImageId:          vmRequest.ImageId,
				VNetId:           vmRequest.VNetId,
				SubnetId:         vmRequest.SubnetId,
				SecurityGroupIds: vmRequest.SecurityGroupIds,
				DataDiskIds:      vmRequest.DataDiskIds,
				SshKeyId:         vmRequest.SshKeyId,
				Description:      vmRequest.Description,
				VmUserName:       vmRequest.VmUserName,
				VmUserPassword:   vmRequest.VmUserPassword,
				RootDiskType:     vmRequest.RootDiskType,
				RootDiskSize:     vmRequest.RootDiskSize,
				Label:            vmRequest.Label,
				CspResourceId:    vmRequest.CspResourceId,
			}

			if subGroupSize == 0 {
				vmInfo.Name = common.ToLower(vmRequest.Name)
			} else {
				vmInfo.SubGroupId = common.ToLower(vmRequest.Name)
				vmInfo.Name = common.ToLower(vmRequest.Name) + "-" + strconv.Itoa(i)
			}
			vmInfo.Id = vmInfo.Name

			vmConfigs = append(vmConfigs, vmConfig{
				vmInfo:       vmInfo,
				subGroupSize: subGroupSize,
				vmIndex:      i,
			})
		}

		// Update vmStartIndex for next VM request
		vmStartIndex += subGroupSize
	}

	// Create or update MCI object
	if !mciAlreadyExists {
		// Create new MCI object
		if err := createMciObject(nsId, mciId, req, uid); err != nil {
			return nil, fmt.Errorf("failed to create MCI object: %w", err)
		}
	} else {
		// Update existing prepared MCI with creating status
		existingMci.Status = model.StatusCreating
		existingMci.TargetStatus = model.StatusRunning
		existingMci.TargetAction = model.ActionCreate
		existingMci.SystemMessage = "Starting VM provisioning"

		// Update request details that might have changed
		if req.InstallMonAgent != "" {
			existingMci.InstallMonAgent = req.InstallMonAgent
		}
		if req.Description != "" {
			existingMci.Description = req.Description
		}
		if req.Label != nil {
			existingMci.Label = req.Label
		}
		if req.SystemLabel != "" {
			existingMci.SystemLabel = req.SystemLabel
		}
		existingMci.PostCommand = req.PostCommand

		UpdateMciInfo(nsId, *existingMci)
		log.Info().Msgf("Updated prepared MCI '%s' to creating status", mciId)
	}

	// Handle hold option
	if option == "hold" {
		if err := handleHoldOption(nsId, mciId); err != nil {
			return nil, fmt.Errorf("hold option failed: %w", err)
		}
		option = "create"
	}

	// Create VM objects with error collection
	var wg sync.WaitGroup
	var createErrors []error

	log.Info().Msgf("Creating %d VM objects", len(vmConfigs))

	for _, config := range vmConfigs {
		wg.Add(1)
		go func(cfg vmConfig) {
			defer wg.Done()
			if err := createVmObjectSafe(nsId, mciId, &cfg.vmInfo); err != nil {
				errorMu.Lock()
				createErrors = append(createErrors, fmt.Errorf("VM object creation failed for '%s': %w", cfg.vmInfo.Name, err))
				addVmError(&vmObjectErrors, cfg.vmInfo.Name, err.Error(), "object_creation")
				errorMu.Unlock()
			}
		}(config)
	}
	wg.Wait()

	// Check for VM object creation errors
	if len(createErrors) > 0 {
		switch req.PolicyOnPartialFailure {
		case model.PolicyRollback:
			log.Warn().Msgf("VM object creation failed for %d VMs, rolling back entire MCI due to policy=rollback", len(createErrors))
			if cleanupErr := cleanupPartialMci(nsId, mciId); cleanupErr != nil {
				log.Error().Err(cleanupErr).Msg("Failed to cleanup partial MCI")
			}
			return nil, fmt.Errorf("VM object creation failed, MCI rolled back: %v", createErrors)
		case model.PolicyRefine:
			log.Warn().Msgf("VM object creation failed for %d VMs, failed VMs will be refined after MCI creation due to policy=refine", len(createErrors))
			// Refine will be executed after MCI creation is completed
		default: // model.PolicyContinue or empty
			log.Warn().Msgf("VM object creation failed for %d VMs, continuing with partial provisioning due to policy=continue", len(createErrors))
		}

		// Log detailed error information
		for i, err := range createErrors {
			log.Error().Msgf("VM object creation error %d: %v", i+1, err)
		}
	}

	// Create actual VMs with intelligent delay and error handling
	log.Info().Msgf("Creating %d VMs", len(vmConfigs))
	createErrors = createErrors[:0] // Reset error slice

	for i, config := range vmConfigs {
		// Apply intelligent delay to avoid CSP rate limiting
		if i > 0 {
			delay := time.Duration(200*i) * time.Millisecond
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
			time.Sleep(delay)
		}

		vmInfoData, err := GetVmObject(nsId, mciId, config.vmInfo.Id)
		if err != nil {
			return nil, fmt.Errorf("failed to get VM object '%s': %w", config.vmInfo.Id, err)
		}

		wg.Add(1)
		go func(vmData model.TbVmInfo, vmName string) {
			defer wg.Done()
			if err := createVmSafe(nsId, mciId, &vmData, option); err != nil {
				errorMu.Lock()
				createErrors = append(createErrors, fmt.Errorf("VM creation failed for '%s': %w", vmName, err))
				addVmErrorUnsafe(&vmCreateErrors, vmName, err.Error(), "vm_creation")
				errorMu.Unlock()
			}
		}(vmInfoData, config.vmInfo.Id)
	}
	wg.Wait()

	// Check for VM creation errors
	if len(createErrors) > 0 {
		switch req.PolicyOnPartialFailure {
		case model.PolicyRollback:
			log.Error().Msgf("VM creation failed for %d VMs, rolling back entire MCI due to policy=rollback", len(createErrors))
			if cleanupErr := cleanupPartialMci(nsId, mciId); cleanupErr != nil {
				log.Error().Err(cleanupErr).Msg("Failed to cleanup partial MCI")
			}
			return nil, fmt.Errorf("VM creation failed, MCI rolled back: %v", createErrors)
		case model.PolicyRefine:
			log.Warn().Msgf("VM creation failed for %d VMs, failed VMs will be refined after MCI creation due to policy=refine", len(createErrors))
			// Refine will be executed after MCI creation is completed
		default: // model.PolicyContinue or empty
			log.Warn().Msgf("VM creation failed for %d VMs, continuing with partial MCI due to policy=continue", len(createErrors))
		}

		// Log detailed error information
		for i, err := range createErrors {
			log.Error().Msgf("VM creation error %d: %v", i+1, err)
		}

		// Continue with partial MCI unless rollback was requested
		log.Info().Msg("Continuing with partial MCI provisioning")
	}

	// Update MCI status
	mciTmp, err := GetMciObject(nsId, mciId)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCI object after VM creation: %w", err)
	}

	mciStatusTmp, err := GetMciStatus(nsId, mciId)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCI status: %w", err)
	}

	mciTmp.Status = mciStatusTmp.Status
	if mciTmp.TargetStatus == mciTmp.Status {
		mciTmp.TargetStatus = model.StatusComplete
		mciTmp.TargetAction = model.ActionComplete
	}
	UpdateMciInfo(nsId, mciTmp)

	log.Info().Msgf("MCI '%s' has been successfully created with %d VMs", mciId, len(vmConfigs))

	// Install monitoring agent if requested
	if err := handleMonitoringAgent(nsId, mciId, mciTmp, option); err != nil {
		log.Error().Err(err).Msg("Failed to install monitoring agent, but continuing")
	}

	// Execute post-deployment commands
	if err := handlePostCommands(nsId, mciId, mciTmp); err != nil {
		log.Error().Err(err).Msg("Failed to execute post-deployment commands, but continuing")
	}

	// Execute refine action if policy is set to refine and there were failures
	var shouldRefine bool
	if req.PolicyOnPartialFailure == model.PolicyRefine && (len(vmObjectErrors) > 0 || len(vmCreateErrors) > 0) {
		log.Info().Msgf("Executing refine action to cleanup failed VMs in MCI '%s'", mciId)
		if refineResult, err := HandleMciAction(nsId, mciId, model.ActionRefine, true); err != nil {
			log.Error().Err(err).Msg("Failed to execute refine action, but continuing")
		} else {
			log.Info().Msgf("Refine action completed: %s", refineResult)
			shouldRefine = true
		}
	}

	// Get final MCI information
	mciResult, err := GetMciInfo(nsId, mciId)
	if err != nil {
		return nil, fmt.Errorf("failed to get final MCI information: %w", err)
	}

	// Add creation error information if there were any failures
	if len(vmObjectErrors) > 0 || len(vmCreateErrors) > 0 {
		successfulVmCount := totalVmCount - len(vmObjectErrors) - len(vmCreateErrors)
		failedVmCount := len(vmObjectErrors) + len(vmCreateErrors)

		var failureStrategy string
		switch req.PolicyOnPartialFailure {
		case model.PolicyRollback:
			failureStrategy = model.PolicyRollback
		case model.PolicyRefine:
			failureStrategy = model.PolicyRefine
		default: // model.PolicyContinue or empty
			failureStrategy = model.PolicyContinue
		}

		mciResult.CreationErrors = &model.MciCreationErrors{
			VmObjectCreationErrors:  vmObjectErrors,
			VmCreationErrors:        vmCreateErrors,
			TotalVmCount:            totalVmCount,
			SuccessfulVmCount:       successfulVmCount,
			FailedVmCount:           failedVmCount,
			FailureHandlingStrategy: failureStrategy,
		}

		log.Info().Msgf("MCI '%s' creation completed with %d successful VMs out of %d total (strategy: %s, refined: %t)",
			mciId, successfulVmCount, totalVmCount, failureStrategy, shouldRefine)
	} else {
		log.Info().Msgf("MCI '%s' has been successfully created with all %d VMs", mciId, totalVmCount)
	}

	// Record provisioning events to history if there were any failures or if specs have previous failure history
	if err := RecordProvisioningEventsFromMci(nsId, mciResult); err != nil {
		log.Error().Err(err).Msgf("Failed to record provisioning events for MCI '%s', but continuing", mciId)
	}

	return mciResult, nil
}

// CheckMciDynamicReq is func to check request info to create MCI obeject and deploy requested VMs in a dynamic way
func CheckMciDynamicReq(req *model.MciConnectionConfigCandidatesReq) (*model.CheckMciDynamicReqInfo, error) {

	mciReqInfo := model.CheckMciDynamicReqInfo{}

	connectionConfigList, err := common.GetConnConfigList(model.DefaultCredentialHolder, true, true)
	if err != nil {
		err := fmt.Errorf("cannot load ConnectionConfigList in MCI dynamic request check")
		log.Error().Err(err).Msg("")
		return &mciReqInfo, err
	}

	// Find detail info and ConnectionConfigCandidates
	for _, k := range req.CommonSpecs {
		errMessage := ""

		vmReqInfo := model.CheckVmDynamicReqInfo{}

		specInfo, err := resource.GetSpec(model.SystemCommonNs, k)
		if err != nil {
			log.Error().Err(err).Msg("")
			errMessage += "//Failed to get Spec (" + k + ")."
		}

		regionInfo, err := common.GetRegion(specInfo.ProviderName, specInfo.RegionName)
		if err != nil {
			errMessage += "//Failed to get Region (" + specInfo.RegionName + ") for Spec (" + k + ") is not found."
		}

		for _, connectionConfig := range connectionConfigList.Connectionconfig {
			if connectionConfig.ProviderName == specInfo.ProviderName && strings.Contains(connectionConfig.RegionDetail.RegionName, specInfo.RegionName) {
				vmReqInfo.ConnectionConfigCandidates = append(vmReqInfo.ConnectionConfigCandidates, connectionConfig.ConfigName)
			}
		}

		vmReqInfo.Spec = specInfo
		availableImageList, err := resource.GetImagesByRegion(model.SystemCommonNs, specInfo.ProviderName, specInfo.RegionName)
		if err != nil {
			errMessage += "//Failed to search images for Spec (" + k + ")"
		}
		vmReqInfo.Image = availableImageList
		vmReqInfo.Region = regionInfo
		vmReqInfo.SystemMessage = errMessage
		mciReqInfo.ReqCheck = append(mciReqInfo.ReqCheck, vmReqInfo)
	}

	return &mciReqInfo, err
}

// CreateSystemMciDynamic is func to create MCI obeject and deploy requested VMs in a dynamic way
func CreateSystemMciDynamic(option string) (*model.TbMciInfo, error) {
	nsId := model.SystemCommonNs
	req := &model.TbMciDynamicReq{}

	// special purpose MCI
	req.Name = option
	labels := map[string]string{
		model.LabelPurpose: option,
	}
	req.Label = labels
	req.SystemLabel = option
	req.Description = option
	req.InstallMonAgent = "no"

	switch option {
	case "probe":
		connections, err := common.GetConnConfigList(model.DefaultCredentialHolder, true, true)
		if err != nil {
			log.Error().Err(err).Msg("")
			return nil, err
		}
		for _, v := range connections.Connectionconfig {

			vmReq := &model.TbVmDynamicReq{}
			vmReq.CommonImage = "ubuntu22.04"                // temporal default value. will be changed
			vmReq.CommonSpec = "aws-ap-northeast-2-t2-small" // temporal default value. will be changed

			deploymentPlan := model.DeploymentPlan{}
			condition := []model.Operation{}
			condition = append(condition, model.Operation{Operand: v.RegionZoneInfoName})

			log.Debug().Msg(" - v.RegionName: " + v.RegionZoneInfoName)

			deploymentPlan.Filter.Policy = append(deploymentPlan.Filter.Policy, model.FilterCondition{Metric: "region", Condition: condition})
			deploymentPlan.Limit = "1"
			common.PrintJsonPretty(deploymentPlan)

			specList, err := RecommendVm(model.SystemCommonNs, deploymentPlan)
			if err != nil {
				log.Error().Err(err).Msg("")
				return nil, err
			}
			if len(specList) != 0 {
				recommendedSpec := specList[0].Id
				vmReq.CommonSpec = recommendedSpec

				vmReq.Label = labels
				vmReq.Name = vmReq.CommonSpec

				vmReq.RootDiskType = specList[0].RootDiskType
				vmReq.RootDiskSize = specList[0].RootDiskSize
				req.Vm = append(req.Vm, *vmReq)
			}
		}

	default:
		err := fmt.Errorf("Not available option. Try (option=probe)")
		return nil, err
	}
	if req.Vm == nil {
		err := fmt.Errorf("No VM is defined")
		return nil, err
	}

	return CreateMciDynamic("", nsId, req, "")
}

// CreateMciDynamic is func to create MCI obeject and deploy requested VMs in a dynamic way
func CreateMciDynamic(reqID string, nsId string, req *model.TbMciDynamicReq, deployOption string) (*model.TbMciInfo, error) {

	mciReq := model.TbMciReq{}
	mciReq.Name = req.Name
	mciReq.Label = req.Label
	mciReq.SystemLabel = req.SystemLabel
	mciReq.InstallMonAgent = req.InstallMonAgent
	mciReq.Description = req.Description
	mciReq.PostCommand = req.PostCommand
	mciReq.PolicyOnPartialFailure = req.PolicyOnPartialFailure

	emptyMci := &model.TbMciInfo{}
	err := common.CheckString(nsId)
	if err != nil {
		err := fmt.Errorf("invalid namespace. %w", err)
		log.Error().Err(err).Msg("")
		return emptyMci, err
	}
	check, err := CheckMci(nsId, req.Name)
	if err != nil {
		err := fmt.Errorf("invalid mci name. %w", err)
		log.Error().Err(err).Msg("")
		return emptyMci, err
	}
	if check {
		err := fmt.Errorf("The mci " + req.Name + " already exists.")
		return emptyMci, err
	}

	vmSubGroupRequests := req.Vm

	// Create MCI object first with preparing status
	mciInfo := model.TbMciInfo{
		ResourceType:    model.StrMCI,
		Id:              req.Name,
		Uid:             common.GenUid(),
		Name:            req.Name,
		Status:          model.StatusPreparing,
		TargetStatus:    model.StatusPrepared,
		TargetAction:    "",
		InstallMonAgent: req.InstallMonAgent,
		Label:           req.Label,
		SystemLabel:     req.SystemLabel,
		Description:     req.Description,
		PlacementAlgo:   "",
		Vm:              []model.TbVmInfo{},
		PostCommand:     req.PostCommand,
		SystemMessage:   "",
	}

	// Save initial MCI object to kvstore (create new key)
	key := common.GenMciKey(nsId, req.Name, "")
	val, err := json.Marshal(mciInfo)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal MCI info")
		return emptyMci, fmt.Errorf("failed to marshal MCI info: %w", err)
	}

	if err := kvstore.Put(key, string(val)); err != nil {
		log.Error().Err(err).Msg("Failed to store MCI object")
		return emptyMci, fmt.Errorf("failed to store MCI object: %w", err)
	}

	// Store label info
	labels := map[string]string{
		model.LabelManager:     model.StrManager,
		model.LabelNamespace:   nsId,
		model.LabelLabelType:   model.StrMCI,
		model.LabelId:          req.Name,
		model.LabelName:        req.Name,
		model.LabelUid:         mciInfo.Uid,
		model.LabelDescription: req.Description,
	}
	for k, v := range req.Label {
		labels[k] = v
	}

	err = label.CreateOrUpdateLabel(model.StrMCI, mciInfo.Uid, key, labels)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create label for MCI")
		// Continue execution even if label creation fails
	}

	log.Info().Msgf("Created MCI object '%s' with preparing status", req.Name)

	// Pre-calculate all VM configurations that will be created for status tracking
	type vmConfig struct {
		vmInfo       model.TbVmInfo
		subGroupSize int
		vmIndex      int
	}

	var vmConfigs []vmConfig
	vmStartIndex := 1

	// Track start index for each subGroup for consistent naming
	subGroupStartIndex := make(map[string]int)

	// Process VM requests and calculate configurations (without creating VM objects yet)
	for _, vmSubGroupRequest := range vmSubGroupRequests {
		subGroupSize, err := strconv.Atoi(vmSubGroupRequest.SubGroupSize)
		if err != nil {
			subGroupSize = 1
		}

		log.Debug().Msgf("Processing VM request '%s' with subGroupSize: %d", vmSubGroupRequest.Name, subGroupSize)

		// Record start index for this subGroup for consistent naming
		subGroupStartIndex[vmSubGroupRequest.Name] = vmStartIndex

		// Get ConnectionName from CommonSpec (similar to getVmReqFromDynamicReq logic)
		var connectionName string
		if vmSubGroupRequest.ConnectionName != "" {
			// If ConnectionName is explicitly specified, use it
			connectionName = vmSubGroupRequest.ConnectionName
		} else {
			// Get ConnectionName from CommonSpec
			specInfo, err := resource.GetSpec(model.SystemCommonNs, vmSubGroupRequest.CommonSpec)
			if err != nil {
				log.Error().Err(err).Msgf("Failed to find VM specification '%s' for VM '%s'", vmSubGroupRequest.CommonSpec, vmSubGroupRequest.Name)
				return emptyMci, fmt.Errorf("failed to find VM specification '%s' for VM '%s': %w", vmSubGroupRequest.CommonSpec, vmSubGroupRequest.Name, err)
			}
			connectionName = specInfo.ConnectionName
		}

		// Get connection config and validate
		connectionConfig, err := common.GetConnConfig(connectionName)
		if err != nil {
			log.Error().Err(err).Msgf("Cannot retrieve connection config '%s' for VM '%s'", connectionName, vmSubGroupRequest.Name)
			return emptyMci, fmt.Errorf("cannot retrieve connection config '%s' for VM '%s': %w", connectionName, vmSubGroupRequest.Name, err)
		}

		// Build VM configurations (without creating VM objects - just for tracking)
		for i := vmStartIndex; i < subGroupSize+vmStartIndex; i++ {
			vmInfo := model.TbVmInfo{
				ResourceType:     model.StrVM,
				Uid:              common.GenUid(),
				Status:           model.StatusPreparing,
				TargetStatus:     model.StatusPrepared,
				TargetAction:     "",
				ConnectionName:   connectionName,
				ConnectionConfig: connectionConfig,
				Location:         connectionConfig.RegionDetail.Location,
				Description:      vmSubGroupRequest.Description,
				SystemMessage:    "",
			}

			// Set VM name based on subGroup logic
			if subGroupSize == 0 {
				vmInfo.Name = common.ToLower(vmSubGroupRequest.Name)
			} else {
				vmInfo.SubGroupId = common.ToLower(vmSubGroupRequest.Name)
				vmInfo.Name = common.ToLower(vmSubGroupRequest.Name) + "-" + strconv.Itoa(i)
			}
			vmInfo.Id = vmInfo.Name

			// Save VM object to kvstore for status tracking during resource preparation
			if err := CreateVmInfo(nsId, req.Name, vmInfo); err != nil {
				log.Error().Err(err).Msgf("Failed to create VM object %s", vmInfo.Name)
				return emptyMci, err
			}

			// Add VM to configurations and MCI
			vmConfigs = append(vmConfigs, vmConfig{
				vmInfo:       vmInfo,
				subGroupSize: subGroupSize,
				vmIndex:      i,
			})
			mciInfo.Vm = append(mciInfo.Vm, vmInfo)

			log.Debug().Msgf("Created VM object '%s' with preparing status", vmInfo.Name)

		}

		// Update vmStartIndex for next subGroup
		vmStartIndex += subGroupSize
	}

	// Update MCI with VM list (save again with VM list included)
	mciVal, err := json.Marshal(mciInfo)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal updated MCI info with VM list")
		return emptyMci, fmt.Errorf("failed to marshal updated MCI info: %w", err)
	}

	if err := kvstore.Put(key, string(mciVal)); err != nil {
		log.Error().Err(err).Msg("Failed to update MCI object with VM list")
		return emptyMci, fmt.Errorf("failed to update MCI object with VM list: %w", err)
	}

	log.Debug().Msgf("Updated MCI '%s' with %d VM objects", req.Name, len(mciInfo.Vm))

	// Check whether VM names meet requirement and prepare resources
	// Use semaphore for parallel processing with concurrency limit
	const maxConcurrency = 10
	semaphore := make(chan struct{}, maxConcurrency)

	var wg sync.WaitGroup
	var mutex sync.Mutex
	errStr := ""
	preparedVMs := make(map[string]bool)

	for i, k := range vmSubGroupRequests {
		wg.Add(1)
		go func(index int, vmReq model.TbVmDynamicReq) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }() // Release semaphore

			// log VM request details
			log.Debug().Msgf("[%d] VM Request: %+v", index, vmReq)

			err := checkCommonResAvailableForVmDynamicReq(&vmReq, nsId)
			if err != nil {
				log.Error().Err(err).Msgf("[%d] Failed to find common resource for MCI provision", index)
				mutex.Lock()
				errStr += "{[" + strconv.Itoa(index+1) + "] " + err.Error() + "} "

				// Mark all VMs in this subGroup as failed for tracking
				subGroupSize, parseErr := strconv.Atoi(vmReq.SubGroupSize)
				if parseErr != nil {
					subGroupSize = 1
				}

				startIdx := subGroupStartIndex[vmReq.Name]
				for i := startIdx; i < startIdx+subGroupSize; i++ {
					var vmName string
					if subGroupSize == 0 {
						vmName = common.ToLower(vmReq.Name)
					} else {
						vmName = common.ToLower(vmReq.Name) + "-" + strconv.Itoa(i)
					}

					_, getErr := GetVmObject(nsId, req.Name, vmName)
					if getErr == nil {
						UpdateVmStatus(nsId, req.Name, vmName, model.StatusFailed, "", err.Error())
					}
				}
				mutex.Unlock()
			} else {
				// Mark all VMs in this subGroup as successfully prepared
				mutex.Lock()
				subGroupSize, parseErr := strconv.Atoi(vmReq.SubGroupSize)
				if parseErr != nil {
					subGroupSize = 1
				}

				startIdx := subGroupStartIndex[vmReq.Name]
				for i := startIdx; i < startIdx+subGroupSize; i++ {
					var vmName string
					if subGroupSize == 0 {
						vmName = common.ToLower(vmReq.Name)
					} else {
						vmName = common.ToLower(vmReq.Name) + "-" + strconv.Itoa(i)
					}

					preparedVMs[vmName] = true

					// Update VM status to prepared
					_, getErr := GetVmObject(nsId, req.Name, vmName)
					if getErr == nil {
						UpdateVmStatus(nsId, req.Name, vmName, model.StatusPrepared, model.StatusRunning, "Resources prepared successfully")
					}
				}
				mutex.Unlock()
				log.Info().Msgf("[%d] VM subGroup '%s' resources prepared successfully (%d VMs)", index, vmReq.Name, subGroupSize)
			}
		}(i, k)
	}

	wg.Wait()

	// Update MCI status based on VM preparation results
	mciInfoUpdated, err := GetMciObject(nsId, req.Name)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get MCI object for status update")
		return emptyMci, err
	}

	// Count prepared vs failed VMs
	preparedCount := len(preparedVMs)
	totalVmCount := len(vmConfigs) // Total individual VMs, not vmRequests

	if preparedCount == totalVmCount {
		// All VMs prepared successfully
		UpdateMciStatus(nsId, req.Name, model.StatusPrepared, model.StatusRunning, fmt.Sprintf("All %d VMs prepared successfully", totalVmCount))
		log.Info().Msgf("MCI '%s': All %d VMs prepared successfully", req.Name, totalVmCount)
	} else if preparedCount > 0 {
		// Partial preparation success
		UpdateMciStatus(nsId, req.Name, model.StatusPreparing, "", fmt.Sprintf("Partial preparation: %d/%d VMs prepared", preparedCount, totalVmCount))
		log.Warn().Msgf("MCI '%s': Partial preparation: %d/%d VMs prepared", req.Name, preparedCount, totalVmCount)
	} else {
		// All VMs failed preparation
		UpdateMciStatus(nsId, req.Name, model.StatusFailed, "", "All VM resource preparations failed")
		log.Error().Msgf("MCI '%s': All VM resource preparations failed", req.Name)
	}

	// Get updated MCI object for return
	mciInfoUpdated, err = GetMciObject(nsId, req.Name)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get updated MCI object")
		return emptyMci, err
	}

	if errStr != "" {
		err = fmt.Errorf("%s", errStr)
		return &mciInfoUpdated, err
	}

	/*
	 * [NOTE]
	 * 1. Generate default resources first
	 * 2. And then, parallel processing of VM requests
	 */

	// Check if vmRequest has elements
	if len(vmSubGroupRequests) > 0 {
		var allCreatedResources []CreatedResource
		var wg sync.WaitGroup
		var mutex sync.Mutex

		type vmResult struct {
			result *VmReqWithCreatedResources
			err    error
		}
		resultChan := make(chan vmResult, len(vmSubGroupRequests))

		// Process all vmRequests in parallel
		for _, k := range vmSubGroupRequests {
			wg.Add(1)
			go func(vmReq model.TbVmDynamicReq) {
				defer wg.Done()
				result, err := getVmReqFromDynamicReq(reqID, nsId, &vmReq)
				resultChan <- vmResult{result: result, err: err}
			}(k)
		}

		// Wait for all goroutines to complete
		wg.Wait()
		close(resultChan)

		// Collect results and check for errors
		var hasError bool
		var failedVMs []string
		var errorDetails []string
		var successfulVMs []string

		for vmRes := range resultChan {
			if vmRes.err != nil {
				log.Error().Err(vmRes.err).Msg("Failed to prepare resources for dynamic MCI creation")
				hasError = true

				// Extract VM details from error context
				vmName := "unknown"
				if vmRes.result != nil && vmRes.result.VmReq != nil {
					vmName = vmRes.result.VmReq.Name
				}
				failedVMs = append(failedVMs, vmName)
				errorDetails = append(errorDetails, fmt.Sprintf("VM '%s': %s", vmName, vmRes.err.Error()))
			} else {
				// Safely append to the shared mciReq.Vm slice
				mutex.Lock()
				mciReq.Vm = append(mciReq.Vm, *vmRes.result.VmReq)
				allCreatedResources = append(allCreatedResources, vmRes.result.CreatedResources...)
				successfulVMs = append(successfulVMs, vmRes.result.VmReq.Name)
				mutex.Unlock()
			}
		}

		// If there were any errors, rollback all created resources
		if hasError {
			// Count resources by type for detailed rollback info
			resourceSummary := make(map[string]int)
			for _, resource := range allCreatedResources {
				resourceSummary[resource.Type]++
			}

			log.Info().Msgf("Resource preparation failed for %d VM(s): %v", len(failedVMs), failedVMs)
			log.Info().Msgf("Successfully prepared %d VM(s): %v", len(successfulVMs), successfulVMs)
			log.Info().Msgf("Rolling back %d created resources: %+v", len(allCreatedResources), resourceSummary)

			time.Sleep(5 * time.Second)
			rollbackErr := rollbackCreatedResources(nsId, allCreatedResources)

			// Build comprehensive error message
			errorMsg := fmt.Sprintf("MCI '%s' creation failed due to resource preparation errors:\n", req.Name)
			errorMsg += fmt.Sprintf("- Failed VMs (%d): %v\n", len(failedVMs), failedVMs)
			if len(successfulVMs) > 0 {
				errorMsg += fmt.Sprintf("- Successfully prepared VMs (%d): %v\n", len(successfulVMs), successfulVMs)
			}

			if len(allCreatedResources) > 0 {
				errorMsg += fmt.Sprintf("- Rollback attempted for %d resources: ", len(allCreatedResources))
				for resType, count := range resourceSummary {
					errorMsg += fmt.Sprintf("%s(%d) ", resType, count)
				}
				errorMsg += "\n"
			}

			errorMsg += "Detailed errors:\n"
			for _, detail := range errorDetails {
				errorMsg += fmt.Sprintf("  • %s\n", detail)
			}

			if rollbackErr != nil {
				errorMsg += fmt.Sprintf("CRITICAL: Rollback operation failed: %s\n", rollbackErr.Error())
				errorMsg += "Manual cleanup may be required for created resources."
				return emptyMci, fmt.Errorf("%s", errorMsg)
			} else {
				errorMsg += "All created resources have been successfully rolled back."
				return emptyMci, fmt.Errorf("%s", errorMsg)
			}
		}
	}

	// Log the prepared MCI request and update the progress
	common.PrintJsonPretty(mciReq)
	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{
		Title: "Prepared all resources for provisioning MCI: " + mciReq.Name,
		Info:  mciReq, Time: time.Now(),
	})
	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{
		Title: "Start instance provisioning", Time: time.Now(),
	})

	// Run create MCI with the generated MCI request
	option := "create"
	if deployOption == "hold" {
		option = "hold"
	}
	result, err := CreateMci(nsId, &mciReq, option)
	return result, err
}

// ValidateMciDynamicReq is func to validate MCI dynamic request before actual provisioning
func ValidateMciDynamicReq(reqID string, nsId string, req *model.TbMciDynamicReq, deployOption string) (*model.ReviewMciDynamicReqInfo, error) {
	return ReviewMciDynamicReq(reqID, nsId, req, deployOption)
}

// ReviewMciDynamicReq is func to review and validate MCI dynamic request comprehensively
func ReviewMciDynamicReq(reqID string, nsId string, req *model.TbMciDynamicReq, deployOption string) (*model.ReviewMciDynamicReqInfo, error) {

	log.Debug().Msgf("Starting MCI dynamic request review for: %s", req.Name)

	// Calculate total VM count considering SubGroupSize
	totalVmCount := 0
	for _, vmReq := range req.Vm {
		subGroupSize := 1 // default
		if vmReq.SubGroupSize != "" {
			if size, err := strconv.Atoi(vmReq.SubGroupSize); err == nil && size > 0 {
				subGroupSize = size
			}
		}
		totalVmCount += subGroupSize
	}

	reviewResult := &model.ReviewMciDynamicReqInfo{
		MciName:      req.Name,
		TotalVmCount: totalVmCount,
		VmReviews:    make([]model.ReviewVmDynamicReqInfo, 0),
		ResourceSummary: model.ReviewResourceSummary{
			UniqueSpecs:     make([]string, 0),
			UniqueImages:    make([]string, 0),
			ConnectionNames: make([]string, 0),
			ProviderNames:   make([]string, 0),
			RegionNames:     make([]string, 0),
		},
		Recommendations:        make([]string, 0),
		PolicyOnPartialFailure: req.PolicyOnPartialFailure,
	}

	// Basic validation
	err := common.CheckString(nsId)
	if err != nil {
		return nil, fmt.Errorf("invalid namespace: %w", err)
	}

	// Check if MCI name is valid and doesn't exist
	check, err := CheckMci(nsId, req.Name)
	if err != nil {
		return nil, fmt.Errorf("invalid mci name: %w", err)
	}
	if check {
		reviewResult.OverallStatus = "Error"
		reviewResult.OverallMessage = fmt.Sprintf("MCI '%s' already exists in namespace '%s'", req.Name, nsId)
		reviewResult.CreationViable = false
		return reviewResult, nil
	}

	if len(req.Vm) == 0 {
		reviewResult.OverallStatus = "Error"
		reviewResult.OverallMessage = "No VM requests provided"
		reviewResult.CreationViable = false
		return reviewResult, nil
	}

	// Track resource summary with thread-safe maps
	specMap := make(map[string]bool)
	imageMap := make(map[string]bool)
	connectionMap := make(map[string]bool)
	providerMap := make(map[string]bool)
	regionMap := make(map[string]bool)

	// Use semaphore for parallel processing with concurrency limit
	const maxConcurrency = 10
	semaphore := make(chan struct{}, maxConcurrency)

	// Channel to collect VM review results
	vmReviewChan := make(chan struct {
		index    int
		vmReview model.ReviewVmDynamicReqInfo
		specInfo *model.TbSpecInfo
		viable   bool
		warning  bool
		cost     float64
	}, len(req.Vm))

	// WaitGroup to wait for all goroutines to complete
	var wg sync.WaitGroup

	// Validate each VM request in parallel
	for i, vmReq := range req.Vm {
		wg.Add(1)
		go func(index int, vmRequest model.TbVmDynamicReq) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			vmReview := model.ReviewVmDynamicReqInfo{
				VmName:       vmRequest.Name,
				SubGroupSize: vmRequest.SubGroupSize,
				CanCreate:    true,
				Status:       "Ready",
				Info:         make([]string, 0),
				Warnings:     make([]string, 0),
				Errors:       make([]string, 0),
			}

			viable := true
			hasVmWarning := false
			var specInfoPtr *model.TbSpecInfo
			vmCost := 0.0

			// Validate VM name
			if vmRequest.Name == "" {
				vmReview.Warnings = append(vmReview.Warnings, "VM SubGroup name not specified, will be auto-generated")
				hasVmWarning = true
			}

			// Validate SubGroupSize
			if vmRequest.SubGroupSize == "" {
				vmRequest.SubGroupSize = "1"
				vmReview.Warnings = append(vmReview.Warnings, "SubGroupSize not specified, defaulting to 1")
				hasVmWarning = true
			}

			// Validate CommonSpec
			specInfo, err := resource.GetSpec(model.SystemCommonNs, vmRequest.CommonSpec)
			if err != nil {
				vmReview.Errors = append(vmReview.Errors, fmt.Sprintf("Failed to get spec '%s': %v", vmRequest.CommonSpec, err))
				vmReview.SpecValidation = model.ReviewResourceValidation{
					ResourceId:  vmRequest.CommonSpec,
					IsAvailable: false,
					Status:      "Unavailable",
					Message:     err.Error(),
				}
				vmReview.CanCreate = false
				viable = false
			} else {
				specInfoPtr = &specInfo
				vmReview.ConnectionName = specInfo.ConnectionName
				vmReview.ProviderName = specInfo.ProviderName
				vmReview.RegionName = specInfo.RegionName

				// Check if spec is available in CSP
				cspSpec, err := resource.LookupSpec(specInfo.ConnectionName, specInfo.CspSpecName)
				if err != nil {
					vmReview.Errors = append(vmReview.Errors, fmt.Sprintf("Spec '%s' not available in CSP: %v", vmRequest.CommonSpec, err))
					vmReview.SpecValidation = model.ReviewResourceValidation{
						ResourceId:    vmRequest.CommonSpec,
						ResourceName:  specInfo.CspSpecName,
						IsAvailable:   false,
						Status:        "Unavailable",
						Message:       err.Error(),
						CspResourceId: specInfo.CspSpecName,
					}
					vmReview.CanCreate = false
					viable = false
				} else {
					vmReview.SpecValidation = model.ReviewResourceValidation{
						ResourceId:    vmRequest.CommonSpec,
						ResourceName:  specInfo.CspSpecName,
						IsAvailable:   true,
						Status:        "Available",
						CspResourceId: cspSpec.Name,
					}

					// Add cost estimation if available
					if specInfo.CostPerHour > 0 {
						vmReview.EstimatedCost = fmt.Sprintf("$%.4f/hour", specInfo.CostPerHour)
						vmCost = float64(specInfo.CostPerHour)
					} else {
						vmReview.EstimatedCost = "Cost estimation unavailable"
					}
				}
			}

			// Validate CommonImage
			if specInfoPtr != nil {
				cspImage, err := resource.LookupImage(specInfoPtr.ConnectionName, vmRequest.CommonImage)
				if err != nil {
					vmReview.Errors = append(vmReview.Errors, fmt.Sprintf("Image '%s' not available in CSP: %v", vmRequest.CommonImage, err))
					vmReview.ImageValidation = model.ReviewResourceValidation{
						ResourceId:    vmRequest.CommonImage,
						IsAvailable:   false,
						Status:        "Unavailable",
						Message:       err.Error(),
						CspResourceId: vmRequest.CommonImage,
					}
					vmReview.CanCreate = false
					viable = false
				} else {
					vmReview.ImageValidation = model.ReviewResourceValidation{
						ResourceId:    vmRequest.CommonImage,
						ResourceName:  cspImage.Name,
						IsAvailable:   true,
						Status:        "Available",
						CspResourceId: cspImage.IId.SystemId,
					}
				}
			}

			// Validate ConnectionName if specified
			if vmRequest.ConnectionName != "" {
				_, err := common.GetConnConfig(vmRequest.ConnectionName)
				if err != nil {
					vmReview.Warnings = append(vmReview.Warnings, fmt.Sprintf("Specified connection '%s' not found, will use default from spec", vmRequest.ConnectionName))
					hasVmWarning = true
				} else {
					vmReview.ConnectionName = vmRequest.ConnectionName
				}
			}

			// Validate RootDisk settings
			if vmRequest.RootDiskType != "" && vmRequest.RootDiskType != "default" {
				vmReview.Info = append(vmReview.Info, fmt.Sprintf("Root disk type configured: %s, be sure it's supported by the provider", vmRequest.RootDiskType))
			}
			if vmRequest.RootDiskSize != "" && vmRequest.RootDiskSize != "default" {
				vmReview.Info = append(vmReview.Info, fmt.Sprintf("Root disk size configured: %s GB, be sure it meets minimum requirements", vmRequest.RootDiskSize))
			}

			// Check provisioning history and risk analysis
			if specInfoPtr != nil {
				riskLevel, riskMessage, err := AnalyzeProvisioningRisk(vmRequest.CommonSpec, vmRequest.CommonImage)
				if err != nil {
					log.Warn().Err(err).Msgf("Failed to analyze provisioning risk for VM: %s", vmRequest.Name)
					vmReview.Warnings = append(vmReview.Warnings, "Failed to analyze provisioning history")
				} else {
					switch riskLevel {
					case "high":
						vmReview.Errors = append(vmReview.Errors, fmt.Sprintf("High provisioning failure risk: %s", riskMessage))
						vmReview.CanCreate = false
						viable = false
						log.Debug().Msgf("High risk detected for spec %s with image %s: %s", vmRequest.CommonSpec, vmRequest.CommonImage, riskMessage)
					case "medium":
						vmReview.Warnings = append(vmReview.Warnings, fmt.Sprintf("Moderate provisioning failure risk: %s", riskMessage))
						hasVmWarning = true
						log.Debug().Msgf("Medium risk detected for spec %s with image %s: %s", vmRequest.CommonSpec, vmRequest.CommonImage, riskMessage)
					case "low":
						if riskMessage != "No previous provisioning history available" && riskMessage != "No provisioning attempts recorded" {
							vmReview.Info = append(vmReview.Info, fmt.Sprintf("Provisioning history: %s", riskMessage))
						}
						log.Debug().Msgf("Low risk for spec %s with image %s: %s", vmRequest.CommonSpec, vmRequest.CommonImage, riskMessage)
					default:
						log.Debug().Msgf("Unknown risk level for spec %s: %s", vmRequest.CommonSpec, riskLevel)
					}
				}
			}

			// Set VM review status
			if len(vmReview.Errors) > 0 {
				vmReview.Status = "Error"
				vmReview.Message = fmt.Sprintf("VM has %d error(s) that prevent creation", len(vmReview.Errors))
			} else if len(vmReview.Warnings) > 0 {
				vmReview.Status = "Warning"
				vmReview.Message = fmt.Sprintf("VM can be created but has %d warning(s)", len(vmReview.Warnings))
			} else {
				vmReview.Status = "Ready"
				vmReview.Message = "VM can be created successfully"
			}

			// Send result to channel
			vmReviewChan <- struct {
				index    int
				vmReview model.ReviewVmDynamicReqInfo
				specInfo *model.TbSpecInfo
				viable   bool
				warning  bool
				cost     float64
			}{
				index:    index,
				vmReview: vmReview,
				specInfo: specInfoPtr,
				viable:   viable,
				warning:  hasVmWarning,
				cost:     vmCost,
			}

			log.Debug().Msgf("[%d] VM '%s' review completed: %s", index, vmRequest.Name, vmReview.Status)
		}(i, vmReq)
	}

	// Close channel when all goroutines are done
	go func() {
		wg.Wait()
		close(vmReviewChan)
	}()

	// Collect results and maintain order
	vmReviews := make([]model.ReviewVmDynamicReqInfo, len(req.Vm))
	allViable := true
	hasWarnings := false
	totalEstimatedCost := 0.0
	vmWithUnknownCost := 0

	// Process results from channel
	for result := range vmReviewChan {
		// Store VM review result in correct order
		vmReviews[result.index] = result.vmReview

		// Update overall status flags
		if !result.viable {
			allViable = false
		}
		if result.warning {
			hasWarnings = true
		}

		// Update cost calculation
		if result.cost > 0 {
			totalEstimatedCost += result.cost
		} else if result.vmReview.EstimatedCost == "Cost estimation unavailable" {
			vmWithUnknownCost++
		}

		// Update resource summary maps (thread-safe since we're processing sequentially here)
		if result.specInfo != nil {
			specMap[req.Vm[result.index].CommonSpec] = true
			connectionMap[result.specInfo.ConnectionName] = true
			providerMap[result.specInfo.ProviderName] = true
			regionMap[result.specInfo.RegionName] = true
		}

		if req.Vm[result.index].CommonImage != "" {
			imageMap[req.Vm[result.index].CommonImage] = true
		}
	}

	// Store VM reviews in result
	reviewResult.VmReviews = vmReviews

	// Build resource summary
	for spec := range specMap {
		reviewResult.ResourceSummary.UniqueSpecs = append(reviewResult.ResourceSummary.UniqueSpecs, spec)
	}
	for image := range imageMap {
		reviewResult.ResourceSummary.UniqueImages = append(reviewResult.ResourceSummary.UniqueImages, image)
	}
	for conn := range connectionMap {
		reviewResult.ResourceSummary.ConnectionNames = append(reviewResult.ResourceSummary.ConnectionNames, conn)
	}
	for provider := range providerMap {
		reviewResult.ResourceSummary.ProviderNames = append(reviewResult.ResourceSummary.ProviderNames, provider)
	}
	for region := range regionMap {
		reviewResult.ResourceSummary.RegionNames = append(reviewResult.ResourceSummary.RegionNames, region)
	}

	reviewResult.ResourceSummary.TotalProviders = len(providerMap)
	reviewResult.ResourceSummary.TotalRegions = len(regionMap)

	// Count available/unavailable resources
	for _, vmReview := range reviewResult.VmReviews {
		if vmReview.SpecValidation.IsAvailable {
			reviewResult.ResourceSummary.AvailableSpecs++
		} else {
			reviewResult.ResourceSummary.UnavailableSpecs++
		}
		if vmReview.ImageValidation.IsAvailable {
			reviewResult.ResourceSummary.AvailableImages++
		} else {
			reviewResult.ResourceSummary.UnavailableImages++
		}
	}

	// Set overall status and cost estimation
	if totalEstimatedCost > 0 {
		if vmWithUnknownCost > 0 {
			reviewResult.EstimatedCost = fmt.Sprintf("$%.4f/hour (partial - %d VMs have unknown costs)", totalEstimatedCost, vmWithUnknownCost)
		} else {
			reviewResult.EstimatedCost = fmt.Sprintf("$%.4f/hour", totalEstimatedCost)
		}
	} else if vmWithUnknownCost > 0 {
		reviewResult.EstimatedCost = fmt.Sprintf("Cost estimation unavailable for all %d VMs", vmWithUnknownCost)
	}

	reviewResult.CreationViable = allViable

	if !allViable {
		reviewResult.OverallStatus = "Error"
		reviewResult.OverallMessage = fmt.Sprintf("MCI cannot be created due to critical errors in VM configurations (Providers: %v, Regions: %v)",
			reviewResult.ResourceSummary.ProviderNames, reviewResult.ResourceSummary.RegionNames)
		reviewResult.Recommendations = append(reviewResult.Recommendations, "Fix all VM configuration errors before attempting to create MCI")
	} else if hasWarnings {
		reviewResult.OverallStatus = "Warning"
		reviewResult.OverallMessage = fmt.Sprintf("MCI can be created but has some configuration warnings (Providers: %v, Regions: %v)",
			reviewResult.ResourceSummary.ProviderNames, reviewResult.ResourceSummary.RegionNames)
		reviewResult.Recommendations = append(reviewResult.Recommendations, "Review and address warnings for optimal configuration")
	} else {
		reviewResult.OverallStatus = "Ready"
		reviewResult.OverallMessage = fmt.Sprintf("All VMs can be created successfully (Providers: %v, Regions: %v)",
			reviewResult.ResourceSummary.ProviderNames, reviewResult.ResourceSummary.RegionNames)
	}

	// Add specific recommendations
	if reviewResult.ResourceSummary.TotalProviders > 3 {
		reviewResult.Recommendations = append(reviewResult.Recommendations, "Consider consolidating to fewer cloud providers to simplify management")
	}
	if reviewResult.ResourceSummary.TotalRegions > 5 {
		reviewResult.Recommendations = append(reviewResult.Recommendations, "Large number of regions may increase latency between VMs")
	}
	if totalEstimatedCost > 10.0 {
		reviewResult.Recommendations = append(reviewResult.Recommendations, "High estimated cost - consider using smaller instance types if appropriate")
	}
	if vmWithUnknownCost > 0 {
		reviewResult.Recommendations = append(reviewResult.Recommendations, fmt.Sprintf("Cost estimation unavailable for %d VMs - actual costs may be higher than shown", vmWithUnknownCost))
	}

	// Add PolicyOnPartialFailure analysis and recommendations
	policy := req.PolicyOnPartialFailure
	if policy == "" {
		policy = model.PolicyContinue // default value
		reviewResult.PolicyOnPartialFailure = model.PolicyContinue
	}

	var policyDescription, policyRecommendation string

	switch policy {
	case model.PolicyContinue:
		policyDescription = "If some VMs fail during creation, MCI will be created with successfully provisioned VMs only. Failed VMs will remain in 'StatusFailed' state and can be fixed later using 'refine' action."
		reviewResult.Recommendations = append(reviewResult.Recommendations,
			"Failure Policy: 'continue' - Partial deployment allowed, failed VMs can be refined later")
		if reviewResult.TotalVmCount > 1 {
			policyRecommendation = "With multiple VMs, consider 'rollback' policy for all-or-nothing deployment, or 'refine' policy for automatic cleanup"
			reviewResult.Recommendations = append(reviewResult.Recommendations,
				"With multiple VMs, partial failures are possible. Consider using 'rollback' policy if you need all-or-nothing deployment, or 'refine' policy for automatic cleanup of failed VMs.")
		}
	case model.PolicyRollback:
		policyDescription = "If any VM fails during creation, the entire MCI will be deleted automatically. This ensures all-or-nothing deployment but may waste resources if only a few VMs fail."
		reviewResult.Recommendations = append(reviewResult.Recommendations,
			"Failure Policy: 'rollback' - All-or-nothing deployment, entire MCI deleted on any failure")
		if reviewResult.TotalVmCount > 5 {
			policyRecommendation = "With many VMs, rollback policy increases risk of complete deployment failure. Consider 'continue' or 'refine' policy for better reliability"
			reviewResult.Recommendations = append(reviewResult.Recommendations,
				"WARNING: With many VMs, rollback policy increases risk of complete deployment failure. Consider 'continue' or 'refine' policy for better reliability.")
		}
		if reviewResult.ResourceSummary.TotalProviders > 2 {
			reviewResult.Recommendations = append(reviewResult.Recommendations,
				"WARNING: Multiple cloud providers increase failure probability. Rollback policy may cause complete deployment failure due to single provider issues.")
		}
	case model.PolicyRefine:
		policyDescription = "If some VMs fail during creation, MCI will be created with successful VMs, and failed VMs will be automatically cleaned up using refine action. This provides the best balance between reliability and resource efficiency."
		reviewResult.Recommendations = append(reviewResult.Recommendations,
			"Failure Policy: 'refine' - Automatic cleanup of failed VMs, optimal balance of reliability and efficiency")
		if reviewResult.TotalVmCount > 10 {
			policyRecommendation = "With many VMs, 'refine' policy provides optimal balance between reliability and resource efficiency"
			reviewResult.Recommendations = append(reviewResult.Recommendations,
				"RECOMMENDED: With many VMs, 'refine' policy provides optimal balance between reliability and resource efficiency.")
		}
	default:
		policyDescription = fmt.Sprintf("Unknown failure policy '%s'. Will default to 'continue'. Valid options: continue, rollback, refine", policy)
		policyRecommendation = "Use one of the valid failure policies: continue, rollback, refine"
		reviewResult.Recommendations = append(reviewResult.Recommendations,
			fmt.Sprintf("WARNING: Unknown failure policy '%s'. Will default to 'continue'. Valid options: continue, rollback, refine", policy))
	}

	reviewResult.PolicyDescription = policyDescription
	reviewResult.PolicyRecommendation = policyRecommendation

	// Add policy-specific warnings based on deployment context
	if reviewResult.OverallStatus == "Warning" && policy == model.PolicyRollback {
		reviewResult.Recommendations = append(reviewResult.Recommendations,
			"CAUTION: Configuration warnings detected with 'rollback' policy. Address warnings to prevent complete deployment failure.")
	}

	if len(reviewResult.ResourceSummary.ProviderNames) > 1 && policy == model.PolicyRollback {
		reviewResult.Recommendations = append(reviewResult.Recommendations,
			"TIP: Multi-cloud deployment with 'rollback' policy is risky. Consider 'refine' policy for better fault tolerance across providers.")
	}

	if deployOption == "hold" {
		reviewResult.Recommendations = append(reviewResult.Recommendations,
			fmt.Sprintf("DEPLOYMENT HOLD: MCI creation will be held for review. Failure policy '%s' will apply when deployment is resumed with control continue.", policy))
	}

	log.Debug().Msgf("MCI review completed: %s - %s (Policy: %s)", reviewResult.OverallStatus, reviewResult.OverallMessage, policy)
	return reviewResult, nil
}

// CreateMciVmDynamic is func to create requested VM in a dynamic way and add it to MCI
func CreateMciVmDynamic(nsId string, mciId string, req *model.TbVmDynamicReq) (*model.TbMciInfo, error) {

	emptyMci := &model.TbMciInfo{}
	subGroupId := req.Name
	check, err := CheckSubGroup(nsId, mciId, subGroupId)
	if err != nil {
		log.Error().Err(err).Msg("")
		return emptyMci, err
	}
	if check {
		err := fmt.Errorf("The name for SubGroup (prefix of VM Id) " + req.Name + " already exists.")
		return emptyMci, err
	}

	err = checkCommonResAvailableForVmDynamicReq(req, nsId)
	if err != nil {
		log.Error().Err(err).Msg("")
		return emptyMci, err
	}

	vmReqResult, err := getVmReqFromDynamicReq("", nsId, req)
	if err != nil {
		log.Error().Err(err).Msg("")
		return emptyMci, err
	}

	return CreateMciGroupVm(nsId, mciId, vmReqResult.VmReq, true)
}

// checkCommonResAvailableForVmDynamicReq is func to check common resources availability for VmDynamicReq
func checkCommonResAvailableForVmDynamicReq(req *model.TbVmDynamicReq, nsId string) error {

	log.Debug().Msgf("Checking common resources for VM Dynamic Request: %+v", req)
	log.Debug().Msgf("Namespace ID: %s", nsId)

	// Get spec info first (required for both spec and image validation)
	specInfo, err := resource.GetSpec(model.SystemCommonNs, req.CommonSpec)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get spec info")
		return fmt.Errorf("failed to get VM specification '%s': %w", req.CommonSpec, err)
	}

	// Channel to collect errors from parallel goroutines
	errorChan := make(chan error, 2)

	// Check spec availability in parallel
	go func() {
		_, err := resource.LookupSpec(specInfo.ConnectionName, specInfo.CspSpecName)
		if err != nil {
			log.Error().Err(err).Msgf("Spec validation failed for %s", specInfo.CspSpecName)
			errorChan <- fmt.Errorf("spec '%s' is not available in connection '%s': %w",
				specInfo.CspSpecName, specInfo.ConnectionName, err)
		} else {
			log.Debug().Msgf("Spec validation successful: %s", specInfo.CspSpecName)
			errorChan <- nil
		}
	}()

	// Check image availability in parallel
	go func() {
		_, err := resource.LookupImage(specInfo.ConnectionName, req.CommonImage)
		if err != nil {
			log.Error().Err(err).Msgf("Image validation failed for %s", req.CommonImage)
			errorChan <- fmt.Errorf("image '%s' is not available in connection '%s': %w",
				req.CommonImage, specInfo.ConnectionName, err)
		} else {
			log.Debug().Msgf("Image validation successful: %s", req.CommonImage)
			errorChan <- nil
		}
	}()

	// Collect errors from both goroutines
	var errorMessages []string
	for i := 0; i < 2; i++ {
		if err := <-errorChan; err != nil {
			errorMessages = append(errorMessages, err.Error())
		}
	}

	// Return combined error if any validation failed
	if len(errorMessages) > 0 {
		combinedError := fmt.Errorf("validation failed for VM '%s': %s",
			req.Name, strings.Join(errorMessages, "; "))
		log.Error().Err(combinedError).Msg("Resource validation failures")
		return combinedError
	}

	log.Debug().Msgf("All resource validations passed for VM: %s", req.Name)
	return nil
}

// getVmReqFromDynamicReq is func to getVmReqFromDynamicReq with created resource tracking
func getVmReqFromDynamicReq(reqID string, nsId string, req *model.TbVmDynamicReq) (*VmReqWithCreatedResources, error) {

	onDemand := true
	var createdResources []CreatedResource

	vmRequest := req
	// Check whether VM names meet requirement.
	k := vmRequest

	vmReq := &model.TbVmReq{}

	specInfo, err := resource.GetSpec(model.SystemCommonNs, req.CommonSpec)
	if err != nil {
		detailedErr := fmt.Errorf("failed to find VM specification '%s': %w. Please verify the spec exists and is properly configured", req.CommonSpec, err)
		log.Error().Err(err).Msgf("Spec lookup failed for VM '%s' with CommonSpec '%s'", req.Name, req.CommonSpec)
		return &VmReqWithCreatedResources{VmReq: &model.TbVmReq{Name: req.Name}, CreatedResources: createdResources}, detailedErr
	}

	// remake vmReqest from given input and check resource availability
	vmReq.ConnectionName = specInfo.ConnectionName

	// If ConnectionName is specified by the request, Use ConnectionName from the request
	if k.ConnectionName != "" {
		vmReq.ConnectionName = k.ConnectionName
	}

	// validate the GetConnConfig for spec
	connection, err := common.GetConnConfig(vmReq.ConnectionName)
	if err != nil {
		detailedErr := fmt.Errorf("failed to get connection configuration '%s' for VM '%s' with spec '%s': %w. Please verify the connection exists and is properly configured",
			vmReq.ConnectionName, req.Name, k.CommonSpec, err)
		log.Error().Err(err).Msgf("Connection config lookup failed for VM '%s', ConnectionName '%s', Spec '%s'", req.Name, vmReq.ConnectionName, k.CommonSpec)
		return &VmReqWithCreatedResources{VmReq: &model.TbVmReq{Name: req.Name, ConnectionName: vmReq.ConnectionName}, CreatedResources: createdResources}, detailedErr
	}

	// Default resource name has this pattern (nsId + "-shared-" + vmReq.ConnectionName)
	resourceName := nsId + model.StrSharedResourceName + vmReq.ConnectionName

	vmReq.SpecId = specInfo.Id
	vmReq.ImageId = k.CommonImage

	// check if the image is available in the CSP
	_, err = resource.LookupImage(connection.ConfigName, vmReq.ImageId)
	if err != nil {
		detailedErr := fmt.Errorf("failed to find image '%s' for VM '%s' in CSP '%s' (connection: %s): %w. Please verify the image exists and is accessible in the target region",
			vmReq.ImageId, req.Name, connection.ProviderName, connection.ConfigName, err)
		log.Error().Err(err).Msgf("Image lookup failed for VM '%s', ImageId '%s', Provider '%s', Connection '%s'",
			req.Name, vmReq.ImageId, connection.ProviderName, connection.ConfigName)
		return &VmReqWithCreatedResources{VmReq: &model.TbVmReq{Name: req.Name, ConnectionName: vmReq.ConnectionName, ImageId: vmReq.ImageId}, CreatedResources: createdResources}, detailedErr
	}
	// Need enhancement to handle custom image request

	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Setting vNet:" + resourceName, Time: time.Now()})

	vmReq.VNetId = resourceName
	_, err = resource.GetResource(nsId, model.StrVNet, vmReq.VNetId)
	if err != nil {
		if !onDemand {
			detailedErr := fmt.Errorf("failed to get required VNet '%s' for VM '%s' from connection '%s': %w. VNet must exist when onDemand is disabled",
				vmReq.VNetId, req.Name, vmReq.ConnectionName, err)
			log.Error().Err(err).Msgf("VNet lookup failed for VM '%s', VNetId '%s', Connection '%s' (onDemand disabled)",
				req.Name, vmReq.VNetId, vmReq.ConnectionName)
			return &VmReqWithCreatedResources{VmReq: &model.TbVmReq{Name: req.Name, ConnectionName: vmReq.ConnectionName, VNetId: vmReq.VNetId}, CreatedResources: createdResources}, detailedErr
		}
		clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Loading default vNet:" + resourceName, Time: time.Now()})

		// Check if the default vNet exists
		_, err := resource.GetResource(nsId, model.StrVNet, vmReq.ConnectionName)
		log.Debug().Msg("checked if the default vNet does NOT exist")
		// Create a new default vNet if it does not exist
		if err != nil && strings.Contains(err.Error(), "does not exist") {
			err2 := resource.CreateSharedResource(nsId, model.StrVNet, vmReq.ConnectionName)
			if err2 != nil {
				detailedErr := fmt.Errorf("failed to create default VNet for VM '%s' in namespace '%s' using connection '%s': %w. This may be due to CSP quotas, permissions, or network configuration issues",
					req.Name, nsId, vmReq.ConnectionName, err2)
				log.Error().Err(err2).Msgf("VNet creation failed for VM '%s', VNetId '%s', Namespace '%s', Connection '%s'",
					req.Name, vmReq.VNetId, nsId, vmReq.ConnectionName)
				return &VmReqWithCreatedResources{VmReq: &model.TbVmReq{Name: req.Name, ConnectionName: vmReq.ConnectionName, VNetId: vmReq.VNetId}, CreatedResources: createdResources}, detailedErr
			} else {
				log.Info().Msg("Created new default vNet: " + vmReq.VNetId)
				// Track the newly created VNet
				createdResources = append(createdResources, CreatedResource{Type: model.StrVNet, Id: vmReq.VNetId})
			}
		}
	} else {
		log.Info().Msg("Found and utilize default vNet: " + vmReq.VNetId)
	}
	vmReq.SubnetId = resourceName

	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Setting SSHKey:" + resourceName, Time: time.Now()})
	vmReq.SshKeyId = resourceName
	_, err = resource.GetResource(nsId, model.StrSSHKey, vmReq.SshKeyId)
	if err != nil {
		if !onDemand {
			detailedErr := fmt.Errorf("failed to get required SSHKey '%s' for VM '%s' from connection '%s': %w. SSHKey must exist when onDemand is disabled",
				vmReq.SshKeyId, req.Name, vmReq.ConnectionName, err)
			log.Error().Err(err).Msgf("SSHKey lookup failed for VM '%s', SshKeyId '%s', Connection '%s' (onDemand disabled)",
				req.Name, vmReq.SshKeyId, vmReq.ConnectionName)
			return &VmReqWithCreatedResources{VmReq: &model.TbVmReq{Name: req.Name, ConnectionName: vmReq.ConnectionName, SshKeyId: vmReq.SshKeyId}, CreatedResources: createdResources}, detailedErr
		}
		clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Loading default SSHKey:" + resourceName, Time: time.Now()})

		// Check if the default SSHKey exists
		_, err := resource.GetResource(nsId, model.StrSSHKey, vmReq.ConnectionName)
		log.Debug().Msg("checked if the default SSHKey does NOT exist")
		// Create a new default SSHKey if it does not exist
		if err != nil && strings.Contains(err.Error(), "does not exist") {
			err2 := resource.CreateSharedResource(nsId, model.StrSSHKey, vmReq.ConnectionName)
			if err2 != nil {
				detailedErr := fmt.Errorf("failed to create default SSHKey for VM '%s' in namespace '%s' using connection '%s': %w. This may be due to CSP quotas, permissions, or key generation issues",
					req.Name, nsId, vmReq.ConnectionName, err2)
				log.Error().Err(err2).Msgf("SSHKey creation failed for VM '%s', SshKeyId '%s', Namespace '%s', Connection '%s'",
					req.Name, vmReq.SshKeyId, nsId, vmReq.ConnectionName)
				return &VmReqWithCreatedResources{VmReq: &model.TbVmReq{Name: req.Name, ConnectionName: vmReq.ConnectionName, SshKeyId: vmReq.SshKeyId}, CreatedResources: createdResources}, detailedErr
			} else {
				log.Info().Msg("Created new default SSHKey: " + vmReq.SshKeyId)
				// Track the newly created SSHKey
				createdResources = append(createdResources, CreatedResource{Type: model.StrSSHKey, Id: vmReq.SshKeyId})
			}
		}
	} else {
		log.Info().Msg("Found and utilize default SSHKey: " + vmReq.SshKeyId)
	}

	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Setting securityGroup:" + resourceName, Time: time.Now()})
	securityGroup := resourceName
	vmReq.SecurityGroupIds = append(vmReq.SecurityGroupIds, securityGroup)
	_, err = resource.GetResource(nsId, model.StrSecurityGroup, securityGroup)
	if err != nil {
		if !onDemand {
			detailedErr := fmt.Errorf("failed to get required SecurityGroup '%s' for VM '%s' from connection '%s': %w. SecurityGroup must exist when onDemand is disabled",
				securityGroup, req.Name, vmReq.ConnectionName, err)
			log.Error().Err(err).Msgf("SecurityGroup lookup failed for VM '%s', SecurityGroup '%s', Connection '%s' (onDemand disabled)",
				req.Name, securityGroup, vmReq.ConnectionName)
			return &VmReqWithCreatedResources{VmReq: &model.TbVmReq{Name: req.Name, ConnectionName: vmReq.ConnectionName, SecurityGroupIds: []string{securityGroup}}, CreatedResources: createdResources}, detailedErr
		}
		clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Loading default securityGroup:" + resourceName, Time: time.Now()})

		// Check if the default security group exists
		_, err := resource.GetResource(nsId, model.StrSecurityGroup, vmReq.ConnectionName)
		// Create a new default security group if it does not exist
		log.Debug().Msg("checked if the default security group does NOT exist")
		if err != nil && strings.Contains(err.Error(), "does not exist") {
			err2 := resource.CreateSharedResource(nsId, model.StrSecurityGroup, vmReq.ConnectionName)
			if err2 != nil {
				detailedErr := fmt.Errorf("failed to create default SecurityGroup for VM '%s' in namespace '%s' using connection '%s': %w. This may be due to CSP quotas, permissions, or firewall rule configuration issues",
					req.Name, nsId, vmReq.ConnectionName, err2)
				log.Error().Err(err2).Msgf("SecurityGroup creation failed for VM '%s', SecurityGroup '%s', Namespace '%s', Connection '%s'",
					req.Name, securityGroup, nsId, vmReq.ConnectionName)
				return &VmReqWithCreatedResources{VmReq: &model.TbVmReq{Name: req.Name, ConnectionName: vmReq.ConnectionName, SecurityGroupIds: []string{securityGroup}}, CreatedResources: createdResources}, detailedErr
			} else {
				log.Info().Msg("Created new default securityGroup: " + securityGroup)
				// Track the newly created SecurityGroup
				createdResources = append(createdResources, CreatedResource{Type: model.StrSecurityGroup, Id: securityGroup})
			}
		}
	} else {
		log.Info().Msg("Found and utilize default securityGroup: " + securityGroup)
	}

	vmReq.Name = k.Name
	if vmReq.Name == "" {
		vmReq.Name = common.GenUid()
	}
	vmReq.Label = k.Label
	vmReq.SubGroupSize = k.SubGroupSize
	vmReq.Description = k.Description
	vmReq.RootDiskType = k.RootDiskType
	vmReq.RootDiskSize = k.RootDiskSize
	vmReq.VmUserPassword = k.VmUserPassword

	common.PrintJsonPretty(vmReq)
	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Prepared resources for VM:" + vmReq.Name, Info: vmReq, Time: time.Now()})

	return &VmReqWithCreatedResources{VmReq: vmReq, CreatedResources: createdResources}, nil
}

// CreateVmObject is func to add VM to MCI
func CreateVmObject(wg *sync.WaitGroup, nsId string, mciId string, vmInfoData *model.TbVmInfo) error {
	log.Debug().Msg("Start to add VM To MCI")
	//goroutin
	defer wg.Done()

	key := common.GenMciKey(nsId, mciId, "")
	keyValue, err := kvstore.GetKv(key)
	if err != nil {
		log.Fatal().Err(err).Msg("AddVmToMci kvstore.GetKv() returned an error.")
		return err
	}
	if keyValue == (kvstore.KeyValue{}) {
		return fmt.Errorf("AddVmToMci Cannot find mciId. Key: %s", key)
	}

	configTmp, err := common.GetConnConfig(vmInfoData.ConnectionName)
	if err != nil {
		log.Error().Err(err).Msg("")
		return err
	}
	vmInfoData.Location = configTmp.RegionDetail.Location

	// Make VM object
	key = common.GenMciKey(nsId, mciId, vmInfoData.Id)
	val, _ := json.Marshal(vmInfoData)
	err = kvstore.Put(key, string(val))
	if err != nil {
		log.Error().Err(err).Msg("")
		return err
	}

	return nil
}

// CreateVm is func to create VM (option = "register" for register existing VM)
func CreateVm(wg *sync.WaitGroup, nsId string, mciId string, vmInfoData *model.TbVmInfo, option string) error {
	log.Info().Msgf("Start to create VM: %s", vmInfoData.Name)
	//goroutin
	defer wg.Done()

	var err error = nil
	switch {
	case vmInfoData.Name == "":
		err = fmt.Errorf("vmInfoData.Name is empty")
	case vmInfoData.ImageId == "":
		err = fmt.Errorf("vmInfoData.ImageId is empty")
	case vmInfoData.ConnectionName == "":
		err = fmt.Errorf("vmInfoData.ConnectionName is empty")
	case vmInfoData.SshKeyId == "":
		err = fmt.Errorf("vmInfoData.SshKeyId is empty")
	case vmInfoData.SpecId == "":
		err = fmt.Errorf("vmInfoData.SpecId is empty")
	case vmInfoData.SecurityGroupIds == nil:
		err = fmt.Errorf("vmInfoData.SecurityGroupIds is empty")
	case vmInfoData.VNetId == "":
		err = fmt.Errorf("vmInfoData.VNetId is empty")
	case vmInfoData.SubnetId == "":
		err = fmt.Errorf("vmInfoData.SubnetId is empty")
	default:
	}
	if err != nil {
		vmInfoData.Status = model.StatusFailed
		vmInfoData.SystemMessage = err.Error()
		UpdateVmInfo(nsId, mciId, *vmInfoData)
		log.Error().Err(err).Msg("")
		return err
	}

	vmKey := common.GenMciKey(nsId, mciId, vmInfoData.Id)

	// in case of registering existing CSP VM
	if option == "register" {
		// CspResourceId is required
		if vmInfoData.CspResourceId == "" {
			err := fmt.Errorf("vmInfoData.CspResourceId is empty (required for register VM)")
			vmInfoData.Status = model.StatusFailed
			vmInfoData.SystemMessage = err.Error()
			UpdateVmInfo(nsId, mciId, *vmInfoData)
			log.Error().Err(err).Msg("")
			return err
		}
	}

	var callResult model.SpiderVMInfo

	// Fill VM creation reqest (request to cb-spider)
	requestBody := model.SpiderVMReqInfoWrapper{}
	requestBody.ConnectionName = vmInfoData.ConnectionName

	//generate VM ID(Name) to request to CSP(Spider)
	requestBody.ReqInfo.Name = vmInfoData.Uid

	customImageFlag := false

	requestBody.ReqInfo.VMUserId = vmInfoData.VmUserName
	requestBody.ReqInfo.VMUserPasswd = vmInfoData.VmUserPassword
	// provide a random passwd, if it is not provided by user (the passwd required for Windows)
	if requestBody.ReqInfo.VMUserPasswd == "" {
		// assign random string (mixed Uid style)
		requestBody.ReqInfo.VMUserPasswd = common.GenRandomPassword(14)
	}

	requestBody.ReqInfo.RootDiskType = vmInfoData.RootDiskType
	requestBody.ReqInfo.RootDiskSize = vmInfoData.RootDiskSize

	if option == "register" {
		requestBody.ReqInfo.CSPid = vmInfoData.CspResourceId

	} else {
		// Try lookup customImage
		requestBody.ReqInfo.ImageName, err = resource.GetCspResourceName(nsId, model.StrCustomImage, vmInfoData.ImageId)
		if requestBody.ReqInfo.ImageName == "" || err != nil {
			log.Debug().Msgf("Not found %s from CustomImage in ns: %s, Use the ImageName directly", vmInfoData.ImageId, nsId)
			// If the image is not a custom image, use the requested image name directly
			requestBody.ReqInfo.ImageName = vmInfoData.ImageId
		} else {
			customImageFlag = true
			requestBody.ReqInfo.ImageType = model.MyImage
			// If the requested image is a custom image (generated by VM snapshot), RootDiskType should be empty.
			// TB ignore inputs for RootDiskType, RootDiskSize
			requestBody.ReqInfo.RootDiskType = ""
			requestBody.ReqInfo.RootDiskSize = ""
		}

		requestBody.ReqInfo.VMSpecName, err = resource.GetCspResourceName(nsId, model.StrSpec, vmInfoData.SpecId)
		if requestBody.ReqInfo.VMSpecName == "" || err != nil {
			log.Warn().Msgf("Not found the Spec: %s in nsId: %s, find it from SystemCommonNs", vmInfoData.SpecId, nsId)
			errAgg := err.Error()
			// If cannot find the resource, use common resource
			requestBody.ReqInfo.VMSpecName, err = resource.GetCspResourceName(model.SystemCommonNs, model.StrSpec, vmInfoData.SpecId)
			log.Info().Msgf("Use the common VMSpecName: %s", requestBody.ReqInfo.VMSpecName)

			if requestBody.ReqInfo.VMSpecName == "" || err != nil {
				errAgg += err.Error()
				err = fmt.Errorf(errAgg)

				vmInfoData.Status = model.StatusFailed
				vmInfoData.SystemMessage = err.Error()
				UpdateVmInfo(nsId, mciId, *vmInfoData)
				log.Error().Err(err).Msg("")

				return err
			}
		}

		requestBody.ReqInfo.VPCName, err = resource.GetCspResourceName(nsId, model.StrVNet, vmInfoData.VNetId)
		if requestBody.ReqInfo.VPCName == "" {
			log.Error().Err(err).Msg("")
			return err
		}

		// retrieve csp subnet id
		subnetInfo, err := resource.GetSubnet(nsId, vmInfoData.VNetId, vmInfoData.SubnetId)
		if err != nil {
			log.Error().Err(err).Msg("Cannot find the Subnet ID: " + vmInfoData.SubnetId)
			vmInfoData.Status = model.StatusFailed
			vmInfoData.SystemMessage = err.Error()
			UpdateVmInfo(nsId, mciId, *vmInfoData)
			return err
		}

		requestBody.ReqInfo.SubnetName = subnetInfo.CspResourceName
		if requestBody.ReqInfo.SubnetName == "" {
			vmInfoData.Status = model.StatusFailed
			vmInfoData.SystemMessage = err.Error()
			UpdateVmInfo(nsId, mciId, *vmInfoData)
			log.Error().Err(err).Msg("")
			return err
		}

		var SecurityGroupIdsTmp []string
		for _, v := range vmInfoData.SecurityGroupIds {
			CspResourceId, err := resource.GetCspResourceName(nsId, model.StrSecurityGroup, v)
			if CspResourceId == "" {
				vmInfoData.Status = model.StatusFailed
				vmInfoData.SystemMessage = err.Error()
				UpdateVmInfo(nsId, mciId, *vmInfoData)
				log.Error().Err(err).Msg("")
				return err
			}

			SecurityGroupIdsTmp = append(SecurityGroupIdsTmp, CspResourceId)
		}
		requestBody.ReqInfo.SecurityGroupNames = SecurityGroupIdsTmp

		var DataDiskIdsTmp []string
		for _, v := range vmInfoData.DataDiskIds {
			// ignore DataDiskIds == "", assume it is ignorable mistake
			if v != "" {
				CspResourceId, err := resource.GetCspResourceName(nsId, model.StrDataDisk, v)
				if err != nil || CspResourceId == "" {
					vmInfoData.Status = model.StatusFailed
					vmInfoData.SystemMessage = err.Error()
					UpdateVmInfo(nsId, mciId, *vmInfoData)
					log.Error().Err(err).Msg("")
					return err
				}
				DataDiskIdsTmp = append(DataDiskIdsTmp, CspResourceId)
			}
		}
		requestBody.ReqInfo.DataDiskNames = DataDiskIdsTmp

		requestBody.ReqInfo.KeyPairName, err = resource.GetCspResourceName(nsId, model.StrSSHKey, vmInfoData.SshKeyId)
		if requestBody.ReqInfo.KeyPairName == "" {
			vmInfoData.Status = model.StatusFailed
			vmInfoData.SystemMessage = err.Error()
			UpdateVmInfo(nsId, mciId, *vmInfoData)
			log.Error().Err(err).Msg("")
			return err
		}
	}

	log.Info().Msg("VM request body to CB-Spider")
	common.PrintJsonPretty(requestBody)

	// Randomly sleep within 20 Secs to avoid rateLimit from CSP
	common.RandomSleep(0, 20)
	client := resty.New()
	method := "POST"
	client.SetTimeout(20 * time.Minute)

	url := model.SpiderRestUrl + "/vm"
	if option == "register" {
		url = model.SpiderRestUrl + "/regvm"
	}

	err = clientManager.ExecuteHttpRequest(
		client,
		method,
		url,
		nil,
		clientManager.SetUseBody(requestBody),
		&requestBody,
		&callResult,
		clientManager.MediumDuration,
	)

	if err != nil {
		err = fmt.Errorf("%v", err)
		vmInfoData.Status = model.StatusFailed
		vmInfoData.SystemMessage = err.Error()
		UpdateVmInfo(nsId, mciId, *vmInfoData)
		msg := fmt.Sprintf("Failed to create VM %s request body to Spider: %v", vmInfoData.Name, requestBody)
		log.Error().Err(err).Msg(msg)
		return err
	}

	vmInfoData.AddtionalDetails = callResult.KeyValueList
	vmInfoData.VmUserName = callResult.VMUserId
	vmInfoData.VmUserPassword = callResult.VMUserPasswd
	vmInfoData.CspResourceName = callResult.IId.NameId
	vmInfoData.CspResourceId = callResult.IId.SystemId
	vmInfoData.Region = callResult.Region
	vmInfoData.PublicIP = callResult.PublicIP
	vmInfoData.SSHPort, _ = TrimIP(callResult.SSHAccessPoint)
	vmInfoData.PublicDNS = callResult.PublicDNS
	vmInfoData.PrivateIP = callResult.PrivateIP
	vmInfoData.PrivateDNS = callResult.PrivateDNS
	vmInfoData.RootDiskType = callResult.RootDiskType
	vmInfoData.RootDiskSize = callResult.RootDiskSize
	vmInfoData.RootDiskName = callResult.RootDiskName
	vmInfoData.NetworkInterface = callResult.NetworkInterface

	vmInfoData.CspSpecName = callResult.VMSpecName
	vmInfoData.CspImageName = callResult.ImageIId.SystemId
	vmInfoData.CspVNetId = callResult.VpcIID.SystemId
	vmInfoData.CspSubnetId = callResult.SubnetIID.SystemId
	vmInfoData.CspSshKeyId = callResult.KeyPairIId.SystemId

	if option == "register" {

		// Reconstuct resource IDs
		// vNet
		resourceListInNs, err := resource.ListResource(nsId, model.StrVNet, "cspResourceName", callResult.VpcIID.NameId)
		if err != nil {
			log.Error().Err(err).Msg("")
		} else {
			resourcesInNs := resourceListInNs.([]model.TbVNetInfo) // type assertion
			for _, resource := range resourcesInNs {
				if resource.ConnectionName == requestBody.ConnectionName {
					vmInfoData.VNetId = resource.Id
					//vmInfoData.SubnetId = resource.SubnetInfoList
				}
			}
		}

		// access Key
		resourceListInNs, err = resource.ListResource(nsId, model.StrSSHKey, "cspResourceName", callResult.KeyPairIId.NameId)
		if err != nil {
			log.Error().Err(err).Msg("")
		} else {
			resourcesInNs := resourceListInNs.([]model.TbSshKeyInfo) // type assertion
			for _, resource := range resourcesInNs {
				if resource.ConnectionName == requestBody.ConnectionName {
					vmInfoData.SshKeyId = resource.Id
				}
			}
		}

	} else {

		if customImageFlag == false {
			resource.UpdateAssociatedObjectList(nsId, model.StrImage, vmInfoData.ImageId, model.StrAdd, vmKey)
		} else {
			resource.UpdateAssociatedObjectList(nsId, model.StrCustomImage, vmInfoData.ImageId, model.StrAdd, vmKey)
		}

		//resource.UpdateAssociatedObjectList(nsId, model.StrSpec, vmInfoData.SpecId, model.StrAdd, vmKey)
		resource.UpdateAssociatedObjectList(nsId, model.StrSSHKey, vmInfoData.SshKeyId, model.StrAdd, vmKey)
		resource.UpdateAssociatedObjectList(nsId, model.StrVNet, vmInfoData.VNetId, model.StrAdd, vmKey)

		for _, v := range vmInfoData.SecurityGroupIds {
			resource.UpdateAssociatedObjectList(nsId, model.StrSecurityGroup, v, model.StrAdd, vmKey)
		}

		for _, v := range vmInfoData.DataDiskIds {
			resource.UpdateAssociatedObjectList(nsId, model.StrDataDisk, v, model.StrAdd, vmKey)
		}
	}

	// Register dataDisks which are created with the creation of VM
	for _, v := range callResult.DataDiskIIDs {
		tbDataDiskReq := model.TbDataDiskReq{
			Name:           v.NameId,
			ConnectionName: vmInfoData.ConnectionName,
			CspResourceId:  v.SystemId,
		}

		dataDisk, err := resource.CreateDataDisk(nsId, &tbDataDiskReq, "register")
		if err != nil {
			err = fmt.Errorf("after starting VM %s, failed to register dataDisk %s. \n", vmInfoData.Name, v.NameId)
			log.Err(err).Msg("")
		}

		vmInfoData.DataDiskIds = append(vmInfoData.DataDiskIds, dataDisk.Id)

		resource.UpdateAssociatedObjectList(nsId, model.StrDataDisk, dataDisk.Id, model.StrAdd, vmKey)
	}

	// Assign a Bastion if none (randomly)
	_, err = SetBastionNodes(nsId, mciId, vmInfoData.Id, "")
	if err != nil {
		// just log error and continue
		log.Debug().Msg(err.Error())
	}

	// set initial TargetAction, TargetStatus
	vmInfoData.TargetAction = model.ActionComplete
	vmInfoData.TargetStatus = model.StatusComplete

	// get and set current vm status
	vmStatusInfoTmp, err := FetchVmStatus(nsId, mciId, vmInfoData.Id)

	if err != nil {
		err = fmt.Errorf("cannot Fetch Vm Status from CSP: %v", err)
		vmInfoData.Status = model.StatusFailed
		vmInfoData.SystemMessage = err.Error()
		UpdateVmInfo(nsId, mciId, *vmInfoData)

		log.Error().Err(err).Msg("")

		return err
	}

	vmInfoData.Status = vmStatusInfoTmp.Status

	// Monitoring Agent Installation Status (init: notInstalled)
	vmInfoData.MonAgentStatus = "notInstalled"
	vmInfoData.NetworkAgentStatus = "notInstalled"

	// set CreatedTime
	t := time.Now()
	vmInfoData.CreatedTime = t.Format("2006-01-02 15:04:05")
	log.Debug().Msg(vmInfoData.CreatedTime)

	UpdateVmInfo(nsId, mciId, *vmInfoData)

	// Store label info using CreateOrUpdateLabel
	labels := map[string]string{
		model.LabelManager:         model.StrManager,
		model.LabelNamespace:       nsId,
		model.LabelLabelType:       model.StrVM,
		model.LabelId:              vmInfoData.Id,
		model.LabelName:            vmInfoData.Name,
		model.LabelUid:             vmInfoData.Uid,
		model.LabelCspResourceId:   vmInfoData.CspResourceId,
		model.LabelCspResourceName: vmInfoData.CspResourceName,
		model.LabelSubGroupId:      vmInfoData.SubGroupId,
		model.LabelMciId:           mciId,
		model.LabelCreatedTime:     vmInfoData.CreatedTime,
		model.LabelConnectionName:  vmInfoData.ConnectionName,
	}
	for key, value := range vmInfoData.Label {
		labels[key] = value
	}
	err = label.CreateOrUpdateLabel(model.StrVM, vmInfoData.Uid, vmKey, labels)
	if err != nil {
		err = fmt.Errorf("cannot create label object: %v", err)
		vmInfoData.Status = model.StatusFailed
		vmInfoData.SystemMessage = err.Error()
		UpdateVmInfo(nsId, mciId, *vmInfoData)

		log.Error().Err(err).Msg("")
		return err
	}

	return nil
}

func filterCheckMciDynamicReqInfoToCheckK8sClusterDynamicReqInfo(mciDReqInfo *model.CheckMciDynamicReqInfo) *model.CheckK8sClusterDynamicReqInfo {
	k8sDReqInfo := model.CheckK8sClusterDynamicReqInfo{}

	if mciDReqInfo != nil {
		for _, k := range mciDReqInfo.ReqCheck {
			if strings.Contains(k.Spec.InfraType, model.StrK8s) ||
				strings.Contains(k.Spec.InfraType, model.StrKubernetes) {

				imageListForK8s := []model.TbImageInfo{}
				for _, i := range k.Image {
					if strings.Contains(i.InfraType, model.StrK8s) ||
						strings.Contains(i.InfraType, model.StrKubernetes) {
						imageListForK8s = append(imageListForK8s, i)
					}
				}

				nodeDReqInfo := model.CheckNodeDynamicReqInfo{
					ConnectionConfigCandidates: k.ConnectionConfigCandidates,
					Spec:                       k.Spec,
					Region:                     k.Region,
					SystemMessage:              k.SystemMessage,
				}

				if len(imageListForK8s) > 0 {
					nodeDReqInfo.Image = imageListForK8s
				} else {
					// No available image because some CSP(ex. azure) can not specify an image
					nodeDReqInfo.Image = []model.TbImageInfo{{Id: "default", Name: "default"}}
				}

				k8sDReqInfo.ReqCheck = append(k8sDReqInfo.ReqCheck, nodeDReqInfo)
			}
		}
	}

	return &k8sDReqInfo
}

// CheckK8sClusterDynamicReq is func to check request info to create K8sCluster obeject and deploy requested Nodes in a dynamic way
func CheckK8sClusterDynamicReq(req *model.K8sClusterConnectionConfigCandidatesReq) (*model.CheckK8sClusterDynamicReqInfo, error) {
	if len(req.CommonSpecs) != 1 {
		err := fmt.Errorf("Only one CommonSpec should be defined.")
		log.Error().Err(err).Msg("")
		return &model.CheckK8sClusterDynamicReqInfo{}, err
	}

	mciCCCReq := model.MciConnectionConfigCandidatesReq{
		CommonSpecs: req.CommonSpecs,
	}
	mciDReqInfo, err := CheckMciDynamicReq(&mciCCCReq)

	k8sDReqInfo := filterCheckMciDynamicReqInfoToCheckK8sClusterDynamicReqInfo(mciDReqInfo)

	return k8sDReqInfo, err
}

func getK8sRecommendVersion(providerName, regionName, reqVersion string) (string, error) {
	availableVersion, err := common.GetAvailableK8sVersion(providerName, regionName)
	if err != nil {
		err := fmt.Errorf("No available K8sCluster version.")
		log.Error().Err(err).Msg("")
		return "", err
	}

	recVersion := model.StrEmpty
	versionIdList := []string{}

	if reqVersion == "" {
		for _, verDetail := range *availableVersion {
			versionIdList = append(versionIdList, verDetail.Id)
			filteredRecVersion := common.FilterDigitsAndDots(recVersion)
			filteredAvailVersion := common.FilterDigitsAndDots(verDetail.Id)
			if common.CompareVersions(filteredRecVersion, filteredAvailVersion) < 0 {
				recVersion = verDetail.Id
			}
		}
	} else {
		for _, verDetail := range *availableVersion {
			versionIdList = append(versionIdList, verDetail.Id)
			if strings.EqualFold(reqVersion, verDetail.Id) {
				recVersion = verDetail.Id
				break
			} else {
				availVersion := common.FilterDigitsAndDots(verDetail.Id)
				filteredReqVersion := common.FilterDigitsAndDots(reqVersion)
				if strings.HasPrefix(availVersion, filteredReqVersion) {
					recVersion = availVersion
					break
				}
			}
		}
	}

	if strings.EqualFold(recVersion, model.StrEmpty) {
		return "", fmt.Errorf("Available K8sCluster Version(k8sclusterinfo.yaml) for Provider/Region(%s/%s): %s",
			providerName, regionName, strings.Join(versionIdList, ", "))
	}

	return recVersion, nil
}

// checkCommonResAvailableForK8sClusterDynamicReq is func to check common resources availability for K8sClusterDynamicReq
func checkCommonResAvailableForK8sClusterDynamicReq(dReq *model.TbK8sClusterDynamicReq) error {
	specInfo, err := resource.GetSpec(model.SystemCommonNs, dReq.CommonSpec)
	if err != nil {
		log.Error().Err(err).Msg("")
		return err
	}

	connName := specInfo.ConnectionName
	// If ConnectionName is specified by the request, Use ConnectionName from the request
	if dReq.ConnectionName != "" {
		connName = dReq.ConnectionName
	}

	// validate the GetConnConfig for spec
	connConfig, err := common.GetConnConfig(connName)
	if err != nil {
		err := fmt.Errorf("Failed to get ConnectionName (" + connName + ") for Spec (" + dReq.CommonSpec + ") is not found.")
		log.Error().Err(err).Msg("")
		return err
	}

	niDesignation, err := common.GetK8sNodeImageDesignation(connConfig.ProviderName)
	if err != nil {
		log.Error().Err(err).Msg("")
	}

	if niDesignation == false {
		// if node image designation is not supported by CSP, CommonImage should be "default" or ""(blank)
		if !(strings.EqualFold(dReq.CommonImage, "default") || strings.EqualFold(dReq.CommonImage, "")) {
			err := fmt.Errorf("The NodeImageDesignation is not supported by CSP(%s). CommonImage's value should be \"default\" or \"\"", connConfig.ProviderName)
			log.Error().Err(err).Msg("")
			return err
		}
	}

	// In K8sCluster, allows dReq.CommonImage to be set to "default" or ""
	if strings.EqualFold(dReq.CommonImage, "default") ||
		strings.EqualFold(dReq.CommonImage, "") {
		// do nothing
	} else {

		// check if the image is available in the CSP
		_, err = resource.LookupImage(dReq.ConnectionName, dReq.CommonImage)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get the Image from the CSP")
			return err
		}

	}

	return nil
}

// checkCommonResAvailableForK8sNodeGroupDynamicReq is func to check common resources availability for K8sNodeGroupDynamicReq
func checkCommonResAvailableForK8sNodeGroupDynamicReq(connName string, dReq *model.TbK8sNodeGroupDynamicReq) error {
	k8sClusterDReq := &model.TbK8sClusterDynamicReq{
		CommonSpec:     dReq.CommonSpec,
		CommonImage:    dReq.CommonImage,
		ConnectionName: connName,
	}

	err := checkCommonResAvailableForK8sClusterDynamicReq(k8sClusterDReq)
	if err != nil {
		return err
	}

	return nil
}

// getK8sClusterReqFromDynamicReq is func to get TbK8sClusterReq from TbK8sClusterDynamicReq
func getK8sClusterReqFromDynamicReq(reqID string, nsId string, dReq *model.TbK8sClusterDynamicReq) (*model.TbK8sClusterReq, error) {
	onDemand := true

	emptyK8sReq := &model.TbK8sClusterReq{}
	k8sReq := &model.TbK8sClusterReq{}
	k8sngReq := &model.TbK8sNodeGroupReq{}

	specInfo, err := resource.GetSpec(model.SystemCommonNs, dReq.CommonSpec)
	if err != nil {
		log.Err(err).Msg("")
		return emptyK8sReq, err
	}
	k8sngReq.SpecId = specInfo.Id

	k8sRecVersion, err := getK8sRecommendVersion(specInfo.ProviderName, specInfo.RegionName, dReq.Version)
	if err != nil {
		log.Err(err).Msg("")
		return emptyK8sReq, err
	}

	// If ConnectionName is specified by the request, Use ConnectionName from the request
	k8sReq.ConnectionName = specInfo.ConnectionName
	if dReq.ConnectionName != "" {
		k8sReq.ConnectionName = dReq.ConnectionName
	}

	// validate the GetConnConfig for spec
	connection, err := common.GetConnConfig(k8sReq.ConnectionName)
	if err != nil {
		err := fmt.Errorf("Failed to Get ConnectionName (" + k8sReq.ConnectionName + ") for Spec (" + dReq.CommonSpec + ") is not found.")
		log.Err(err).Msg("")
		return emptyK8sReq, err
	}

	k8sNgOnCreation, err := common.GetK8sNodeGroupsOnK8sCreation(connection.ProviderName)
	if err != nil {
		log.Err(err).Msgf("Failed to Get Nodegroups on K8sCluster Creation")
		return emptyK8sReq, err
	}

	// In K8sCluster, allows dReq.CommonImage to be set to "default" or ""
	if strings.EqualFold(dReq.CommonImage, "default") ||
		strings.EqualFold(dReq.CommonImage, "") {
		// do nothing
	} else {

		// check if the image is available in the CSP
		_, err = resource.LookupImage(dReq.ConnectionName, dReq.CommonImage)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get the Image from the CSP")
			return emptyK8sReq, err
		}

	}

	// Default resource name has this pattern (nsId + "-shared-" + vmReq.ConnectionName)
	resourceName := nsId + model.StrSharedResourceName + k8sReq.ConnectionName

	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Setting vNet:" + resourceName, Time: time.Now()})

	k8sReq.VNetId = resourceName
	_, err = resource.GetResource(nsId, model.StrVNet, k8sReq.VNetId)
	if err != nil {
		if !onDemand {
			err := fmt.Errorf("Failed to get the vNet " + k8sReq.VNetId + " from " + k8sReq.ConnectionName)
			log.Err(err).Msg("Failed to get the vNet")
			return emptyK8sReq, err
		}

		clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Loading default vNet:" + resourceName, Time: time.Now()})

		err2 := resource.CreateSharedResource(nsId, model.StrVNet, k8sReq.ConnectionName)
		if err2 != nil {
			log.Err(err2).Msg("Failed to create new default vNet " + k8sReq.VNetId + " from " + k8sReq.ConnectionName)
			return emptyK8sReq, err2
		} else {
			log.Info().Msg("Created new default vNet: " + k8sReq.VNetId)
		}
	} else {
		log.Info().Msg("Found and utilize default vNet: " + k8sReq.VNetId)
	}
	k8sReq.SubnetIds = append(k8sReq.SubnetIds, resourceName)
	k8sReq.SubnetIds = append(k8sReq.SubnetIds, resourceName+"-01")

	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Setting SSHKey:" + resourceName, Time: time.Now()})

	k8sngReq.SshKeyId = resourceName
	_, err = resource.GetResource(nsId, model.StrSSHKey, k8sngReq.SshKeyId)
	if err != nil {
		if !onDemand {
			err := fmt.Errorf("Failed to get the SSHKey " + k8sngReq.SshKeyId + " from " + k8sReq.ConnectionName)
			log.Err(err).Msg("Failed to get the SSHKey")
			return emptyK8sReq, err
		}

		clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Loading default SSHKey:" + resourceName, Time: time.Now()})

		err2 := resource.CreateSharedResource(nsId, model.StrSSHKey, k8sReq.ConnectionName)
		if err2 != nil {
			log.Err(err2).Msg("Failed to create new default SSHKey " + k8sngReq.SshKeyId + " from " + k8sReq.ConnectionName)
			return emptyK8sReq, err2
		} else {
			log.Info().Msg("Created new default SSHKey: " + k8sngReq.SshKeyId)
		}
	} else {
		log.Info().Msg("Found and utilize default SSHKey: " + k8sngReq.SshKeyId)
	}

	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Setting securityGroup:" + resourceName, Time: time.Now()})

	securityGroup := resourceName
	k8sReq.SecurityGroupIds = append(k8sReq.SecurityGroupIds, securityGroup)
	_, err = resource.GetResource(nsId, model.StrSecurityGroup, securityGroup)
	if err != nil {
		if !onDemand {
			err := fmt.Errorf("Failed to get the securityGroup " + securityGroup + " from " + k8sReq.ConnectionName)
			log.Err(err).Msg("Failed to get the securityGroup")
			return emptyK8sReq, err
		}

		clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Loading default securityGroup:" + resourceName, Time: time.Now()})

		err2 := resource.CreateSharedResource(nsId, model.StrSecurityGroup, k8sReq.ConnectionName)
		if err2 != nil {
			log.Err(err2).Msg("Failed to create new default securityGroup " + securityGroup + " from " + k8sReq.ConnectionName)
			return emptyK8sReq, err2
		} else {
			log.Info().Msg("Created new default securityGroup: " + securityGroup)
		}
	} else {
		log.Info().Msg("Found and utilize default securityGroup: " + securityGroup)
	}

	k8sngReq.Name = dReq.NodeGroupName
	if k8sngReq.Name == "" {
		k8sngReq.Name = common.GenUid()
	}
	k8sngReq.RootDiskType = dReq.RootDiskType
	k8sngReq.RootDiskSize = dReq.RootDiskSize
	k8sngReq.OnAutoScaling = dReq.OnAutoScaling
	if k8sngReq.OnAutoScaling == "" {
		k8sngReq.OnAutoScaling = "true"
	}
	k8sngReq.DesiredNodeSize = dReq.DesiredNodeSize
	if k8sngReq.DesiredNodeSize == "" {
		k8sngReq.DesiredNodeSize = "1"
	}
	k8sngReq.MinNodeSize = dReq.MinNodeSize
	if k8sngReq.MinNodeSize == "" {
		k8sngReq.MinNodeSize = "1"
	}
	k8sngReq.MaxNodeSize = dReq.MaxNodeSize
	if k8sngReq.MaxNodeSize == "" {
		k8sngReq.MaxNodeSize = "2"
	}
	k8sReq.Description = dReq.Description
	k8sReq.Name = dReq.Name
	if k8sReq.Name == "" {
		k8sReq.Name = common.GenUid()
	}
	k8sReq.Version = k8sRecVersion
	if k8sNgOnCreation {
		k8sReq.K8sNodeGroupList = append(k8sReq.K8sNodeGroupList, *k8sngReq)
	} else {
		log.Info().Msg("Need to Add NodeGroups To Use This K8sCluster")
	}
	k8sReq.Label = dReq.Label

	common.PrintJsonPretty(k8sReq)
	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Prepared resources for K8sCluster:" + k8sReq.Name, Info: k8sReq, Time: time.Now()})

	return k8sReq, nil
}

// CreateK8sClusterDynamic is func to create K8sCluster obeject and deploy requested K8sCluster and NodeGroup in a dynamic way
func CreateK8sClusterDynamic(reqID string, nsId string, dReq *model.TbK8sClusterDynamicReq, deployOption string) (*model.TbK8sClusterInfo, error) {
	emptyK8sCluster := &model.TbK8sClusterInfo{}
	err := common.CheckString(nsId)
	if err != nil {
		log.Err(err).Msg("")
		return emptyK8sCluster, err
	}
	check, err := resource.CheckK8sCluster(nsId, dReq.Name)
	if err != nil {
		log.Err(err).Msg("")
		return emptyK8sCluster, err
	}
	if check {
		err := fmt.Errorf("already exists")
		log.Err(err).Msgf("Failed to Create K8sCluster(%s) Dynamically", dReq.Name)
		return emptyK8sCluster, err
	}

	err = checkCommonResAvailableForK8sClusterDynamicReq(dReq)
	if err != nil {
		log.Err(err).Msgf("Failed to find common resource for K8sCluster provision")
		return emptyK8sCluster, err
	}

	//If not, generate default resources dynamically.
	k8sReq, err := getK8sClusterReqFromDynamicReq(reqID, nsId, dReq)
	if err != nil {
		log.Err(err).Msg("Failed to get shared resources for dynamic K8sCluster creation")
		return emptyK8sCluster, err
	}
	/*
		  FIXME: need to improve a rollback process
			if err != nil {
				log.Err(err).Msg("Failed to prefare resources for dynamic K8sCluster creation")
				// Rollback created default resources
				time.Sleep(5 * time.Second)
				log.Info().Msg("Try rollback created default resources")
				rollbackResult, rollbackErr := resource.DelAllSharedResources(nsId)
				if rollbackErr != nil {
					err = fmt.Errorf("Failed in rollback operation: %w", rollbackErr)
				} else {
					ids := strings.Join(rollbackResult.IdList, ", ")
					err = fmt.Errorf("Rollback results [%s]: %w", ids, err)
				}
				return emptyK8sCluster, err
			}
	*/

	common.PrintJsonPretty(k8sReq)
	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Prepared all resources for provisioning K8sCluster:" + k8sReq.Name, Info: k8sReq, Time: time.Now()})
	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Start provisioning", Time: time.Now()})

	// Run create K8sCluster with the generated K8sCluster request (option != register)
	option := "create"
	if deployOption == "hold" {
		option = "hold"
	}

	return resource.CreateK8sCluster(nsId, k8sReq, option)
}

// getK8sNodeGroupReqFromDynamicReq is func to get TbK8sNodeGroupReq from TbK8sNodeGroupDynamicReq
func getK8sNodeGroupReqFromDynamicReq(reqID string, nsId string, k8sClusterInfo *model.TbK8sClusterInfo, dReq *model.TbK8sNodeGroupDynamicReq) (*model.TbK8sNodeGroupReq, error) {
	emptyK8sNgReq := &model.TbK8sNodeGroupReq{}
	k8sNgReq := &model.TbK8sNodeGroupReq{}

	specInfo, err := resource.GetSpec(model.SystemCommonNs, dReq.CommonSpec)
	if err != nil {
		log.Err(err).Msg("")
		return emptyK8sNgReq, err
	}
	k8sNgReq.SpecId = specInfo.Id

	// If ConnectionName for K8sNodeGroup must be same as ConnectionName for K8sCluster
	if specInfo.ConnectionName != k8sClusterInfo.ConnectionName {
		err := fmt.Errorf("ConnectionName(" + specInfo.ConnectionName + ") of K8sNodeGroup Must Match ConnectionName(" + k8sClusterInfo.ConnectionName + ") of K8sCluster")
		log.Err(err).Msg("")
		return emptyK8sNgReq, err
	}

	// In K8sNodeGroup, allows dReq.CommonImage to be set to "default" or ""
	if strings.EqualFold(dReq.CommonImage, "default") ||
		strings.EqualFold(dReq.CommonImage, "") {
		// do nothing
	} else {
		// check if the image is available in the CSP
		_, err = resource.LookupImage(k8sClusterInfo.ConnectionName, dReq.CommonImage)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get the Image from the CSP")
			return emptyK8sNgReq, err
		}
	}

	// Default resource name has this pattern (nsId + "-shared-" + vmReq.ConnectionName)
	resourceName := nsId + model.StrSharedResourceName + k8sClusterInfo.ConnectionName

	k8sNgReq.SshKeyId = resourceName
	_, err = resource.GetResource(nsId, model.StrSSHKey, k8sNgReq.SshKeyId)
	if err != nil {
		err := fmt.Errorf("Failed to get the SSHKey " + k8sNgReq.SshKeyId + " from " + k8sClusterInfo.ConnectionName)
		log.Err(err).Msg("Failed to get the SSHKey")
		return emptyK8sNgReq, err
	} else {
		log.Info().Msg("Found and utilize default SSHKey: " + k8sNgReq.SshKeyId)
	}

	k8sNgReq.Name = dReq.Name
	if k8sNgReq.Name == "" {
		k8sNgReq.Name = common.GenUid()
	}
	k8sNgReq.RootDiskType = dReq.RootDiskType
	k8sNgReq.RootDiskSize = dReq.RootDiskSize
	k8sNgReq.OnAutoScaling = dReq.OnAutoScaling
	if k8sNgReq.OnAutoScaling == "" {
		k8sNgReq.OnAutoScaling = "true"
	}
	k8sNgReq.DesiredNodeSize = dReq.DesiredNodeSize
	if k8sNgReq.DesiredNodeSize == "" {
		k8sNgReq.DesiredNodeSize = "1"
	}
	k8sNgReq.MinNodeSize = dReq.MinNodeSize
	if k8sNgReq.MinNodeSize == "" {
		k8sNgReq.MinNodeSize = "1"
	}
	k8sNgReq.MaxNodeSize = dReq.MaxNodeSize
	if k8sNgReq.MaxNodeSize == "" {
		k8sNgReq.MaxNodeSize = "2"
	}
	k8sNgReq.Description = dReq.Description
	k8sNgReq.Label = dReq.Label

	common.PrintJsonPretty(k8sNgReq)
	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Prepared resources for K8sNodeGroup:" + k8sNgReq.Name, Info: k8sNgReq, Time: time.Now()})

	return k8sNgReq, nil
}

// CreateK8sNodeGroupDynamic is func to create K8sNodeGroup obeject and deploy requested K8sNodeGroup in a dynamic way
func CreateK8sNodeGroupDynamic(reqID string, nsId string, k8sClusterId string, dReq *model.TbK8sNodeGroupDynamicReq) (*model.TbK8sClusterInfo, error) {
	log.Debug().Msgf("reqID: %s, nsId: %s, k8sClusterId: %s, dReq: %v\n", reqID, nsId, k8sClusterId, dReq)

	emptyK8sCluster := &model.TbK8sClusterInfo{}

	check, err := resource.CheckK8sCluster(nsId, k8sClusterId)
	if err != nil {
		log.Err(err).Msg("")
		return emptyK8sCluster, err
	}
	if !check {
		err := fmt.Errorf("K8sCluster(%s) is not existed", k8sClusterId)
		log.Err(err).Msgf("Failed to Create K8sNodeGroup(%s) in K8sCluster(%s) Dynamically", dReq.Name, k8sClusterId)
		return emptyK8sCluster, err
	}

	tbK8sCInfo, err := resource.GetK8sCluster(nsId, k8sClusterId)
	if err != nil {
		log.Err(err).Msgf("Failed to Create K8sNodeGroup(%s) in K8sCluster(%s) Dynamically", dReq.Name, k8sClusterId)
		return emptyK8sCluster, err
	}

	if tbK8sCInfo.Status != model.TbK8sClusterActive {
		err := fmt.Errorf("K8sCluster(%s) is not active status", k8sClusterId)
		log.Err(err).Msgf("Failed to Create K8sNodeGroup(%s) in K8sCluster(%s) Dynamically", dReq.Name, k8sClusterId)
		return emptyK8sCluster, err
	}

	for _, ngi := range tbK8sCInfo.K8sNodeGroupList {
		if ngi.Name == dReq.Name {
			err := fmt.Errorf("K8sNodeGroup(%s) already exists", dReq.Name)
			log.Err(err).Msgf("Failed to Create K8sNodeGroup(%s) in K8sCluster(%s) Dynamically", dReq.Name, k8sClusterId)
			return emptyK8sCluster, err
		}
	}

	err = checkCommonResAvailableForK8sNodeGroupDynamicReq(tbK8sCInfo.ConnectionName, dReq)
	if err != nil {
		log.Err(err).Msgf("Failed to find common resource for K8sNodeGroup provision")
		return emptyK8sCluster, err
	}

	k8sNgReq, err := getK8sNodeGroupReqFromDynamicReq(reqID, nsId, tbK8sCInfo, dReq)
	if err != nil {
		log.Err(err).Msg("Failed to get shared resources for dynamic K8sNodeGroup creation")
		return emptyK8sCluster, err
	}

	common.PrintJsonPretty(k8sNgReq)
	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Prepared all resources for provisioning K8sNodeGroup:" + k8sNgReq.Name, Info: k8sNgReq, Time: time.Now()})
	clientManager.UpdateRequestProgress(reqID, clientManager.ProgressInfo{Title: "Start provisioning", Time: time.Now()})

	return resource.AddK8sNodeGroup(nsId, k8sClusterId, k8sNgReq)
}

// Provisioning History Management Functions

// generateProvisioningLogKey generates kvstore key for provisioning log
// It URL-encodes the specId to handle special characters like "+" safely
func generateProvisioningLogKey(specId string) string {
	// URL encode the specId to handle special characters like "+" in "gcp+europe-north1+f1-micro"
	encodedSpecId := url.QueryEscape(specId)
	return fmt.Sprintf("/log/provision/%s", encodedSpecId)
}

// GetProvisioningLog retrieves provisioning log for a specific spec ID
func GetProvisioningLog(specId string) (*model.ProvisioningLog, error) {
	log.Debug().Msgf("Getting provisioning log for spec: %s", specId)

	key := generateProvisioningLogKey(specId)
	keyValue, err := kvstore.GetKv(key)
	if err != nil {
		if err.Error() == "key not found" {
			log.Debug().Msgf("No provisioning log found for spec: %s", specId)
			return nil, nil // No log exists yet
		}
		log.Error().Err(err).Msgf("Failed to get provisioning log for spec: %s", specId)
		return nil, fmt.Errorf("failed to get provisioning log: %w", err)
	}

	// Check if the value is empty or invalid
	if keyValue.Value == "" {
		log.Debug().Msgf("Empty value found for provisioning log spec: %s, treating as no log exists", specId)
		return nil, nil
	}

	// Check if the value is valid JSON by trying to parse it
	var rawJson json.RawMessage
	if err := json.Unmarshal([]byte(keyValue.Value), &rawJson); err != nil {
		log.Warn().Err(err).Msgf("Invalid JSON found for provisioning log spec: %s, deleting corrupted entry", specId)
		// Delete the corrupted entry
		if deleteErr := kvstore.Delete(key); deleteErr != nil {
			log.Error().Err(deleteErr).Msgf("Failed to delete corrupted provisioning log for spec: %s", specId)
		}
		return nil, nil // Treat as no log exists
	}

	var provisioningLog model.ProvisioningLog
	err = json.Unmarshal([]byte(keyValue.Value), &provisioningLog)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to unmarshal provisioning log for spec: %s", specId)
		// Delete the corrupted entry as a fallback
		if deleteErr := kvstore.Delete(key); deleteErr != nil {
			log.Error().Err(deleteErr).Msgf("Failed to delete corrupted provisioning log for spec: %s", specId)
		}
		return nil, nil // Treat as no log exists instead of returning error
	}

	log.Debug().Msgf("Successfully retrieved provisioning log for spec: %s (failures: %d, successes: %d)",
		specId, provisioningLog.FailureCount, provisioningLog.SuccessCount)
	return &provisioningLog, nil
}

// SaveProvisioningLog saves or updates provisioning log for a specific spec ID
func SaveProvisioningLog(provisioningLog *model.ProvisioningLog) error {
	log.Debug().Msgf("Saving provisioning log for spec: %s", provisioningLog.SpecId)

	provisioningLog.LastUpdated = time.Now()

	key := generateProvisioningLogKey(provisioningLog.SpecId)
	value, err := json.Marshal(provisioningLog)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to marshal provisioning log for spec: %s", provisioningLog.SpecId)
		return fmt.Errorf("failed to marshal provisioning log: %w", err)
	}

	err = kvstore.Put(key, string(value))
	if err != nil {
		log.Error().Err(err).Msgf("Failed to save provisioning log for spec: %s", provisioningLog.SpecId)
		return fmt.Errorf("failed to save provisioning log: %w", err)
	}

	log.Debug().Msgf("Successfully saved provisioning log for spec: %s", provisioningLog.SpecId)
	return nil
}

// DeleteProvisioningLog deletes provisioning log for a specific spec ID
func DeleteProvisioningLog(specId string) error {
	log.Debug().Msgf("Deleting provisioning log for spec: %s", specId)

	key := generateProvisioningLogKey(specId)
	err := kvstore.Delete(key)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to delete provisioning log for spec: %s", specId)
		return fmt.Errorf("failed to delete provisioning log: %w", err)
	}

	log.Debug().Msgf("Successfully deleted provisioning log for spec: %s", specId)
	return nil
}

// RecordProvisioningEvent records a provisioning event (success or failure) to the log
func RecordProvisioningEvent(event *model.ProvisioningEvent) error {
	log.Debug().Msgf("Recording provisioning event for spec: %s, success: %t", event.SpecId, event.IsSuccess)

	// Get existing log or create new one
	existingLog, err := GetProvisioningLog(event.SpecId)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get existing provisioning log for spec: %s", event.SpecId)
		return fmt.Errorf("failed to get existing provisioning log: %w", err)
	}

	var provisioningLog *model.ProvisioningLog
	if existingLog == nil {
		// Create new log if it doesn't exist
		log.Debug().Msgf("Creating new provisioning log for spec: %s", event.SpecId)

		// Get spec info to populate connection details
		specInfo, err := resource.GetSpec(model.SystemCommonNs, event.SpecId)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to get spec info for: %s", event.SpecId)
			return fmt.Errorf("failed to get spec info: %w", err)
		}

		provisioningLog = &model.ProvisioningLog{
			SpecId:            event.SpecId,
			ConnectionName:    specInfo.ConnectionName,
			ProviderName:      specInfo.ProviderName,
			RegionName:        specInfo.RegionName,
			FailureCount:      0,
			SuccessCount:      0,
			FailureTimestamps: make([]time.Time, 0),
			SuccessTimestamps: make([]time.Time, 0),
			FailureMessages:   make([]string, 0),
			FailureImages:     make([]string, 0),
			SuccessImages:     make([]string, 0),
			AdditionalInfo:    make(map[string]string),
		}
	} else {
		provisioningLog = existingLog
	}

	// Record the event
	if event.IsSuccess {
		// Only record success if there were previous failures
		if provisioningLog.FailureCount > 0 {
			log.Debug().Msgf("Recording success event for spec: %s (previous failures exist)", event.SpecId)
			provisioningLog.SuccessCount++
			provisioningLog.SuccessTimestamps = append(provisioningLog.SuccessTimestamps, event.Timestamp)
			if event.CspImageName != "" && !contains(provisioningLog.SuccessImages, event.CspImageName) {
				provisioningLog.SuccessImages = append(provisioningLog.SuccessImages, event.CspImageName)
			}
		} else {
			log.Debug().Msgf("Skipping success event recording for spec: %s (no previous failures)", event.SpecId)
			return nil // Don't record success if no previous failures
		}
	} else {
		// Always record failures
		log.Debug().Msgf("Recording failure event for spec: %s", event.SpecId)
		provisioningLog.FailureCount++
		provisioningLog.FailureTimestamps = append(provisioningLog.FailureTimestamps, event.Timestamp)
		if event.ErrorMessage != "" {
			provisioningLog.FailureMessages = append(provisioningLog.FailureMessages, event.ErrorMessage)
		}
		if event.CspImageName != "" && !contains(provisioningLog.FailureImages, event.CspImageName) {
			provisioningLog.FailureImages = append(provisioningLog.FailureImages, event.CspImageName)
		}
	}

	// Add additional context information
	if event.MciId != "" {
		if provisioningLog.AdditionalInfo == nil {
			provisioningLog.AdditionalInfo = make(map[string]string)
		}
		provisioningLog.AdditionalInfo["lastMciId"] = event.MciId
	}

	// Save the updated log
	err = SaveProvisioningLog(provisioningLog)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to save provisioning log for spec: %s", event.SpecId)
		return fmt.Errorf("failed to save provisioning log: %w", err)
	}

	log.Debug().Msgf("Successfully recorded provisioning event for spec: %s (total failures: %d, successes: %d)",
		event.SpecId, provisioningLog.FailureCount, provisioningLog.SuccessCount)
	return nil
}

// RecordProvisioningEventsFromMci analyzes MCI creation result and records provisioning events
func RecordProvisioningEventsFromMci(nsId string, mciInfo *model.TbMciInfo) error {
	log.Debug().Msgf("Recording provisioning events from MCI: %s", mciInfo.Id)

	if mciInfo.CreationErrors == nil {
		log.Debug().Msgf("No creation errors found in MCI: %s, checking for individual VM failures", mciInfo.Id)
	}

	eventCount := 0

	// Process VMs to record events
	for _, vm := range mciInfo.Vm {
		log.Debug().Msgf("Processing VM: %s, status: %s", vm.Id, vm.Status)

		// Determine if this VM failed or succeeded based on status
		isSuccess := vm.Status == model.StatusRunning
		errorMessage := ""

		if !isSuccess {
			// Look for specific error message in creation errors
			if mciInfo.CreationErrors != nil {
				for _, vmError := range mciInfo.CreationErrors.VmCreationErrors {
					if vmError.VmName == vm.Id || strings.Contains(vmError.VmName, vm.Id) {
						errorMessage = vmError.Error
						break
					}
				}
				// Also check VM object creation errors
				for _, vmError := range mciInfo.CreationErrors.VmObjectCreationErrors {
					if vmError.VmName == vm.Id || strings.Contains(vmError.VmName, vm.Id) {
						errorMessage = vmError.Error
						break
					}
				}
			}
			// If no specific error message found, use a generic one
			if errorMessage == "" {
				errorMessage = fmt.Sprintf("VM creation failed with status: %s", vm.Status)
			}
		}

		// Create provisioning event
		event := &model.ProvisioningEvent{
			SpecId:       vm.SpecId,
			CspImageName: vm.CspImageName,
			IsSuccess:    isSuccess,
			ErrorMessage: errorMessage,
			Timestamp:    time.Now(),
			VmName:       vm.Id,
			MciId:        mciInfo.Id,
		}

		// Record the event
		err := RecordProvisioningEvent(event)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to record provisioning event for VM: %s", vm.Id)
			continue
		}

		eventCount++
		log.Debug().Msgf("Recorded provisioning event for VM: %s, spec: %s, success: %t",
			vm.Id, vm.SpecId, isSuccess)
	}

	log.Debug().Msgf("Successfully recorded %d provisioning events from MCI: %s", eventCount, mciInfo.Id)
	return nil
}

// AnalyzeProvisioningRisk analyzes the risk of provisioning failure based on historical data
func AnalyzeProvisioningRisk(specId string, cspImageName string) (riskLevel string, riskMessage string, err error) {
	log.Debug().Msgf("Analyzing provisioning risk for spec: %s, image: %s", specId, cspImageName)

	// Get detailed risk analysis
	riskAnalysis, err := AnalyzeProvisioningRiskDetailed(specId, cspImageName)
	if err != nil {
		return "low", "Unable to analyze provisioning risk", err
	}

	// Return overall risk for backward compatibility
	return riskAnalysis.OverallRisk.Level, riskAnalysis.OverallRisk.Message, nil
}

// AnalyzeProvisioningRiskDetailed provides comprehensive risk analysis with separate spec and image risk assessment
func AnalyzeProvisioningRiskDetailed(specId string, cspImageName string) (*model.RiskAnalysis, error) {
	log.Debug().Msgf("Analyzing detailed provisioning risk for spec: %s, image: %s", specId, cspImageName)

	// Get provisioning log - now handles corrupted data gracefully
	provisioningLog, err := GetProvisioningLog(specId)
	if err != nil {
		log.Warn().Err(err).Msgf("Failed to get provisioning log for spec: %s, treating as no history", specId)
		// Return default low risk analysis
		return &model.RiskAnalysis{
			SpecRisk: model.SpecRiskInfo{
				Level:   "low",
				Message: "Unable to analyze spec history, assuming low risk",
			},
			ImageRisk: model.ImageRiskInfo{
				Level:            "low",
				Message:          "Unable to analyze image history, assuming low risk",
				IsNewCombination: true,
			},
			OverallRisk: model.OverallRiskInfo{
				Level:             "low",
				Message:           "Unable to analyze provisioning history, assuming low risk",
				PrimaryRiskFactor: "none",
			},
			Recommendations: []string{"Monitor this deployment for any issues"},
		}, nil
	}

	// If no log exists, assume low risk
	if provisioningLog == nil {
		log.Debug().Msgf("No provisioning history found for spec: %s", specId)
		return &model.RiskAnalysis{
			SpecRisk: model.SpecRiskInfo{
				Level:   "low",
				Message: "No previous provisioning history available for this spec",
			},
			ImageRisk: model.ImageRiskInfo{
				Level:            "low",
				Message:          "No previous history for this image with this spec",
				IsNewCombination: true,
			},
			OverallRisk: model.OverallRiskInfo{
				Level:             "low",
				Message:           "No previous provisioning history available",
				PrimaryRiskFactor: "none",
			},
			Recommendations: []string{"This is a new spec, monitor deployment closely"},
		}, nil
	}

	totalAttempts := provisioningLog.FailureCount + provisioningLog.SuccessCount
	if totalAttempts == 0 {
		log.Debug().Msgf("No provisioning attempts recorded for spec: %s", specId)
		return &model.RiskAnalysis{
			SpecRisk: model.SpecRiskInfo{
				Level:   "low",
				Message: "No provisioning attempts recorded for this spec",
			},
			ImageRisk: model.ImageRiskInfo{
				Level:            "low",
				Message:          "No attempts with this image on this spec",
				IsNewCombination: true,
			},
			OverallRisk: model.OverallRiskInfo{
				Level:             "low",
				Message:           "No provisioning attempts recorded",
				PrimaryRiskFactor: "none",
			},
			Recommendations: []string{"First deployment with this configuration, proceed with monitoring"},
		}, nil
	}

	failureRate := float64(provisioningLog.FailureCount) / float64(totalAttempts)

	// Check image-specific history
	imageHasFailed := contains(provisioningLog.FailureImages, cspImageName)
	imageHasSucceeded := contains(provisioningLog.SuccessImages, cspImageName)
	isNewCombination := !imageHasFailed && !imageHasSucceeded

	// Count the number of different images that have failed/succeeded with this spec
	failedImageCount := len(provisioningLog.FailureImages)
	succeededImageCount := len(provisioningLog.SuccessImages)

	log.Debug().Msgf("Provisioning analysis for spec %s: failures=%d, successes=%d, rate=%.2f, image_failed=%t, image_succeeded=%t, failed_images=%d, succeeded_images=%d",
		specId, provisioningLog.FailureCount, provisioningLog.SuccessCount, failureRate, imageHasFailed, imageHasSucceeded, failedImageCount, succeededImageCount)

	// Analyze spec-specific risk
	specRisk := analyzeSpecRisk(failedImageCount, succeededImageCount, provisioningLog.FailureCount, provisioningLog.SuccessCount, failureRate)

	// Analyze image-specific risk
	imageRisk := analyzeImageRisk(imageHasFailed, imageHasSucceeded, isNewCombination, cspImageName)

	// Determine overall risk and primary factor
	overallRisk := determineOverallRisk(specRisk, imageRisk)

	// Generate recommendations
	recommendations := generateRecommendations(specRisk, imageRisk, overallRisk)

	return &model.RiskAnalysis{
		SpecRisk:        specRisk,
		ImageRisk:       imageRisk,
		OverallRisk:     overallRisk,
		Recommendations: recommendations,
	}, nil
}

// analyzeSpecRisk analyzes risk factors specific to the VM specification
func analyzeSpecRisk(failedImageCount, succeededImageCount, totalFailures, totalSuccesses int, failureRate float64) model.SpecRiskInfo {
	var level, message string

	if failedImageCount >= 10 {
		// Very likely spec-level issue: 10+ different images failed
		level = "high"
		message = fmt.Sprintf("Spec-level issue detected: %d different images have failed with this spec (%.1f%% failure rate). This suggests the spec itself may be problematic",
			failedImageCount, failureRate*100)
	} else if failedImageCount >= 5 {
		// Likely spec-level issue: 5+ different images failed
		level = "medium"
		message = fmt.Sprintf("Possible spec-level issue: %d different images have failed with this spec (%.1f%% failure rate). Consider checking spec compatibility",
			failedImageCount, failureRate*100)
	} else if failedImageCount >= 3 && succeededImageCount == 0 {
		// Potential spec-level issue: 3+ different images failed with no successes
		level = "medium"
		message = fmt.Sprintf("Potential spec-level issue: %d different images have failed with this spec and none have succeeded (%.1f%% failure rate)",
			failedImageCount, failureRate*100)
	} else if failureRate >= 0.8 {
		level = "high"
		message = fmt.Sprintf("Very high failure rate (%.1f%%) for this spec, even with some successful images",
			failureRate*100)
	} else if failureRate >= 0.5 {
		level = "medium"
		message = fmt.Sprintf("Moderate failure rate (%.1f%%) for this spec across different images",
			failureRate*100)
	} else if failureRate > 0 {
		level = "low"
		message = fmt.Sprintf("Low failure rate (%.1f%%) for this spec, mostly successful with various images",
			failureRate*100)
	} else {
		level = "low"
		message = "No failures recorded for this spec, appears stable"
	}

	return model.SpecRiskInfo{
		Level:               level,
		Message:             message,
		FailedImageCount:    failedImageCount,
		SucceededImageCount: succeededImageCount,
		TotalFailures:       totalFailures,
		TotalSuccesses:      totalSuccesses,
		FailureRate:         failureRate,
	}
}

// analyzeImageRisk analyzes risk factors specific to the image
func analyzeImageRisk(imageHasFailed, imageHasSucceeded, isNewCombination bool, cspImageName string) model.ImageRiskInfo {
	var level, message string

	if imageHasFailed {
		// CRITICAL: Any previous failure with this exact spec+image combination means high risk
		if !imageHasSucceeded {
			// This specific image has failed before and never succeeded with this spec
			level = "high"
			message = fmt.Sprintf("CRITICAL: This exact spec+image combination (%s) has failed before and never succeeded", cspImageName)
		} else {
			// This image has both failed and succeeded with this spec - still high risk due to failure history
			level = "high"
			message = fmt.Sprintf("HIGH RISK: This exact spec+image combination (%s) has failed at least once before, despite some successes", cspImageName)
		}
	} else if imageHasSucceeded && !imageHasFailed {
		// This image has only succeeded with this spec - safest option
		level = "low"
		message = fmt.Sprintf("SAFE: This exact spec+image combination (%s) has previously succeeded and never failed", cspImageName)
	} else if isNewCombination {
		// This is a new combination - unknown risk
		level = "low"
		message = fmt.Sprintf("NEW: This exact spec+image combination (%s) has never been tried before", cspImageName)
	} else {
		// Fallback case
		level = "low"
		message = "No specific image risk identified"
	}

	return model.ImageRiskInfo{
		Level:                level,
		Message:              message,
		HasFailedWithSpec:    imageHasFailed,
		HasSucceededWithSpec: imageHasSucceeded,
		IsNewCombination:     isNewCombination,
	}
}

// determineOverallRisk determines the overall risk based on spec and image risks
func determineOverallRisk(specRisk model.SpecRiskInfo, imageRisk model.ImageRiskInfo) model.OverallRiskInfo {
	var level, message, primaryRiskFactor string

	// Determine the highest risk level
	specRiskValue := getRiskValue(specRisk.Level)
	imageRiskValue := getRiskValue(imageRisk.Level)

	if specRiskValue >= imageRiskValue {
		level = specRisk.Level
		primaryRiskFactor = "spec"
		if specRiskValue > imageRiskValue {
			message = fmt.Sprintf("Primary risk is spec-related: %s", specRisk.Message)
		} else {
			message = fmt.Sprintf("Both spec and image have similar risk levels. Spec: %s", specRisk.Message)
		}
	} else {
		level = imageRisk.Level
		primaryRiskFactor = "image"
		message = fmt.Sprintf("Primary risk is image-related: %s", imageRisk.Message)
	}

	// Special case handling
	if specRisk.Level == "low" && imageRisk.Level == "low" {
		primaryRiskFactor = "none"
		message = "Both spec and image appear safe based on historical data"
	} else if imageRisk.IsNewCombination && specRisk.Level != "low" {
		primaryRiskFactor = "combination"
		message = fmt.Sprintf("New image combination with a spec that has shown issues: %s", specRisk.Message)
	}

	return model.OverallRiskInfo{
		Level:             level,
		Message:           message,
		PrimaryRiskFactor: primaryRiskFactor,
	}
}

// generateRecommendations provides actionable guidance based on risk analysis
func generateRecommendations(specRisk model.SpecRiskInfo, imageRisk model.ImageRiskInfo, overallRisk model.OverallRiskInfo) []string {
	var recommendations []string

	switch overallRisk.PrimaryRiskFactor {
	case "spec":
		if specRisk.Level == "high" {
			recommendations = append(recommendations, "Consider changing to a different VM specification")
			recommendations = append(recommendations, "Check if this spec is available and properly configured in the target region")
			if specRisk.FailedImageCount >= 5 {
				recommendations = append(recommendations, "Multiple images have failed with this spec - likely a spec-level compatibility issue")
			}
		} else if specRisk.Level == "medium" {
			recommendations = append(recommendations, "Monitor deployment closely - this spec has shown some issues")
			recommendations = append(recommendations, "Consider having a backup spec ready")
		}

	case "image":
		if imageRisk.Level == "high" {
			if imageRisk.HasFailedWithSpec && !imageRisk.HasSucceededWithSpec {
				recommendations = append(recommendations, "CRITICAL: This exact spec+image combination has failed before and NEVER succeeded")
				recommendations = append(recommendations, "STRONGLY RECOMMEND: Use a different image immediately")
				recommendations = append(recommendations, "Find alternative images with same OS/application requirements")
			} else if imageRisk.HasFailedWithSpec && imageRisk.HasSucceededWithSpec {
				recommendations = append(recommendations, "HIGH RISK: This exact combination has failed at least once before")
				recommendations = append(recommendations, "CAUTION: Even though it succeeded sometimes, failure history indicates instability")
				recommendations = append(recommendations, "Consider using a more reliable image or test extensively before production")
			}
		} else if imageRisk.Level == "medium" {
			recommendations = append(recommendations, "This image has mixed results with this spec - proceed with caution")
		}

	case "combination":
		recommendations = append(recommendations, "This is a new spec+image combination")
		recommendations = append(recommendations, "Monitor closely as there's no historical data for this combination")
		if specRisk.Level != "low" {
			recommendations = append(recommendations, "Consider that this spec has shown issues with other images")
		}

	case "none":
		recommendations = append(recommendations, "Both spec and image appear safe based on historical data")
		recommendations = append(recommendations, "Continue with standard monitoring")

	default:
		recommendations = append(recommendations, "Monitor deployment and record results for future analysis")
	}

	// Add critical warnings for any failure history
	if imageRisk.HasFailedWithSpec {
		recommendations = append(recommendations, "IMPORTANT: This exact spec+image combination has failure history - high caution advised")
	}

	// Add general recommendations based on risk levels
	if overallRisk.Level == "high" {
		recommendations = append(recommendations, "HIGH RISK DEPLOYMENT - Consider testing in development environment first")
		recommendations = append(recommendations, "Ensure robust rollback plans and monitoring are in place")
	} else if overallRisk.Level == "medium" {
		recommendations = append(recommendations, "Medium risk - ensure proper monitoring and rollback plans are in place")
	}

	return recommendations
}

// getRiskValue converts risk level to numeric value for comparison
func getRiskValue(riskLevel string) int {
	switch riskLevel {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
} // CleanupCorruptedProvisioningLogs removes all corrupted provisioning log entries from kvstore
func CleanupCorruptedProvisioningLogs() error {
	log.Debug().Msg("Starting cleanup of corrupted provisioning logs")

	// Get all keys with provisioning log prefix
	keyPattern := "/log/provision/"
	keys, err := kvstore.GetKvList(keyPattern)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list provisioning log keys")
		return fmt.Errorf("failed to list provisioning log keys: %w", err)
	}

	cleanupCount := 0
	for _, key := range keys {
		keyValue, err := kvstore.GetKv(key.Key)
		if err != nil {
			log.Warn().Err(err).Msgf("Failed to get value for key: %s", key.Key)
			continue
		}

		// Check if the value is empty or invalid JSON
		if keyValue.Value == "" {
			log.Debug().Msgf("Deleting empty provisioning log: %s", key.Key)
			if deleteErr := kvstore.Delete(key.Key); deleteErr != nil {
				log.Error().Err(deleteErr).Msgf("Failed to delete empty log: %s", key.Key)
			} else {
				cleanupCount++
			}
			continue
		}

		// Test JSON validity
		var testLog model.ProvisioningLog
		if err := json.Unmarshal([]byte(keyValue.Value), &testLog); err != nil {
			log.Debug().Msgf("Deleting corrupted provisioning log: %s", key.Key)
			if deleteErr := kvstore.Delete(key.Key); deleteErr != nil {
				log.Error().Err(deleteErr).Msgf("Failed to delete corrupted log: %s", key.Key)
			} else {
				cleanupCount++
			}
		}
	}

	log.Debug().Msgf("Cleanup completed. Removed %d corrupted provisioning logs", cleanupCount)
	return nil
}

// ValidateProvisioningLogIntegrity checks and repairs provisioning log data integrity
func ValidateProvisioningLogIntegrity(specId string) error {
	log.Debug().Msgf("Validating provisioning log integrity for spec: %s", specId)

	key := generateProvisioningLogKey(specId)
	keyValue, err := kvstore.GetKv(key)
	if err != nil {
		if err.Error() == "key not found" {
			log.Debug().Msgf("No provisioning log found for spec: %s", specId)
			return nil // No log exists, nothing to validate
		}
		return fmt.Errorf("failed to get provisioning log: %w", err)
	}

	// Check if the value is empty
	if keyValue.Value == "" {
		log.Warn().Msgf("Empty provisioning log found for spec: %s, deleting", specId)
		return kvstore.Delete(key)
	}

	// Test JSON validity
	var testLog model.ProvisioningLog
	if err := json.Unmarshal([]byte(keyValue.Value), &testLog); err != nil {
		log.Warn().Msgf("Corrupted provisioning log found for spec: %s, deleting", specId)
		return kvstore.Delete(key)
	}

	// Validate data consistency
	totalAttempts := testLog.FailureCount + testLog.SuccessCount
	if totalAttempts != len(testLog.FailureTimestamps)+len(testLog.SuccessTimestamps) {
		log.Warn().Msgf("Inconsistent timestamp count for spec: %s, repairing", specId)

		// Repair by truncating arrays to match counts
		if len(testLog.FailureTimestamps) > testLog.FailureCount {
			testLog.FailureTimestamps = testLog.FailureTimestamps[:testLog.FailureCount]
		}
		if len(testLog.SuccessTimestamps) > testLog.SuccessCount {
			testLog.SuccessTimestamps = testLog.SuccessTimestamps[:testLog.SuccessCount]
		}

		// Save repaired log
		return SaveProvisioningLog(&testLog)
	}

	log.Debug().Msgf("Provisioning log integrity validated for spec: %s", specId)
	return nil
}
