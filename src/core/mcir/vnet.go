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

// Package mcir is to manage multi-cloud infra resource
package mcir

import (
	"encoding/json"
	"fmt"

	"github.com/cloud-barista/cb-tumblebug/src/core/common"
	validator "github.com/go-playground/validator/v10"
	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
)

// 2020-04-09 https://github.com/cloud-barista/cb-spider/blob/master/cloud-control-manager/cloud-driver/interfaces/resources/VPCHandler.go

// SpiderVPCReqInfoWrapper is a wrapper struct to create JSON body of 'Create VPC request'
type SpiderVPCReqInfoWrapper struct {
	ConnectionName string
	ReqInfo        SpiderVPCReqInfo
}

// SpiderVPCReqInfo is a struct to create JSON body of 'Create VPC request'
type SpiderVPCReqInfo struct {
	Name           string
	IPv4_CIDR      string
	SubnetInfoList []SpiderSubnetReqInfo
	CSPId          string
}

// SpiderSubnetReqInfoWrapper is a wrapper struct to create JSON body of 'Create subnet request'
type SpiderSubnetReqInfoWrapper struct {
	ConnectionName string
	ReqInfo        SpiderSubnetReqInfo
}

// SpiderSubnetReqInfo is a struct to create JSON body of 'Create subnet request'
type SpiderSubnetReqInfo struct {
	Name         string `validate:"required"`
	IPv4_CIDR    string `validate:"required"`
	KeyValueList []common.KeyValue
}

// SpiderVPCInfo is a struct to handle VPC information from the CB-Spider's REST API response
type SpiderVPCInfo struct {
	IId            common.IID // {NameId, SystemId}
	IPv4_CIDR      string
	SubnetInfoList []SpiderSubnetInfo
	KeyValueList   []common.KeyValue
}

// SpiderSubnetInfo is a struct to handle subnet information from the CB-Spider's REST API response
type SpiderSubnetInfo struct {
	IId          common.IID // {NameId, SystemId}
	IPv4_CIDR    string
	KeyValueList []common.KeyValue
}

// TbVNetReq is a struct to handle 'Create vNet' request toward CB-Tumblebug.
type TbVNetReq struct { // Tumblebug
	Name           string        `json:"name" validate:"required"`
	ConnectionName string        `json:"connectionName" validate:"required"`
	CidrBlock      string        `json:"cidrBlock"`
	SubnetInfoList []TbSubnetReq `json:"subnetInfoList"`
	Description    string        `json:"description"`
	CspVNetId      string        `json:"cspVNetId"`
}

// TbVNetReqStructLevelValidation is a function to validate 'TbVNetReq' object.
func TbVNetReqStructLevelValidation(sl validator.StructLevel) {

	u := sl.Current().Interface().(TbVNetReq)

	err := common.CheckString(u.Name)
	if err != nil {
		// ReportError(field interface{}, fieldName, structFieldName, tag, param string)
		sl.ReportError(u.Name, "name", "Name", err.Error(), "")
	}
}

// TbVNetInfo is a struct that represents TB vNet object.
type TbVNetInfo struct { // Tumblebug
	Id                   string            `json:"id"`
	Name                 string            `json:"name"`
	ConnectionName       string            `json:"connectionName"`
	CidrBlock            string            `json:"cidrBlock"`
	SubnetInfoList       []TbSubnetInfo    `json:"subnetInfoList"`
	Description          string            `json:"description"`
	CspVNetId            string            `json:"cspVNetId"`
	CspVNetName          string            `json:"cspVNetName"`
	Status               string            `json:"status"`
	KeyValueList         []common.KeyValue `json:"keyValueList"`
	AssociatedObjectList []string          `json:"associatedObjectList"`
	IsAutoGenerated      bool              `json:"isAutoGenerated"`

	// SystemLabel is for describing the MCIR in a keyword (any string can be used) for special System purpose
	SystemLabel string `json:"systemLabel" example:"Managed by CB-Tumblebug" default:""`

	// Disabled for now
	//Region         string `json:"region"`
	//ResourceGroupName string `json:"resourceGroupName"`
}

// TbSubnetReq is a struct that represents TB subnet object.
type TbSubnetReq struct { // Tumblebug
	Name         string `validate:"required"`
	IPv4_CIDR    string `validate:"required"`
	KeyValueList []common.KeyValue
	Description  string
}

// TbSubnetReqStructLevelValidation is a function to validate 'TbSubnetReq' object.
func TbSubnetReqStructLevelValidation(sl validator.StructLevel) {

	u := sl.Current().Interface().(TbSubnetReq)

	err := common.CheckString(u.Name)
	if err != nil {
		// ReportError(field interface{}, fieldName, structFieldName, tag, param string)
		sl.ReportError(u.Name, "name", "Name", err.Error(), "")
	}
}

// TbSubnetInfo is a struct that represents TB subnet object.
type TbSubnetInfo struct { // Tumblebug
	Id           string
	Name         string `validate:"required"`
	IPv4_CIDR    string `validate:"required"`
	BastionNodes []BastionNode
	KeyValueList []common.KeyValue
	Description  string
}

// BastionNode is a struct that represents TB BastionNode object.
type BastionNode struct {
	McisId string `json:"mcisId"`
	VmId   string `json:"vmId"`
}

// CreateVNet accepts vNet creation request, creates and returns an TB vNet object
func CreateVNet(nsId string, u *TbVNetReq, option string) (TbVNetInfo, error) {
	log.Info().Msg("CreateVNet")
	temp := TbVNetInfo{}
	resourceType := common.StrVNet

	err := common.CheckString(nsId)
	if err != nil {
		log.Error().Err(err).Msg("")
		return temp, err
	}

	err = validate.Struct(u)
	if err != nil {
		if _, ok := err.(*validator.InvalidValidationError); ok {
			return temp, err
		}

		temp := TbVNetInfo{}
		return temp, err
	}

	check, err := CheckResource(nsId, resourceType, u.Name)

	if check {
		err := fmt.Errorf("The vNet " + u.Name + " already exists.")
		return temp, err
	}

	if err != nil {
		err := fmt.Errorf("Failed to check the existence of the vNet " + u.Name + ".")
		return temp, err
	}

	requestBody := SpiderVPCReqInfoWrapper{}
	requestBody.ConnectionName = u.ConnectionName
	requestBody.ReqInfo.Name = fmt.Sprintf("%s-%s", nsId, u.Name)
	requestBody.ReqInfo.IPv4_CIDR = u.CidrBlock
	requestBody.ReqInfo.CSPId = u.CspVNetId

	// requestBody.ReqInfo.SubnetInfoList = u.SubnetInfoList
	for _, v := range u.SubnetInfoList {
		jsonBody, err := json.Marshal(v)
		if err != nil {
			log.Error().Err(err).Msg("")
		}

		spiderSubnetInfo := SpiderSubnetReqInfo{}
		err = json.Unmarshal(jsonBody, &spiderSubnetInfo)
		if err != nil {
			log.Error().Err(err).Msg("")
		}

		requestBody.ReqInfo.SubnetInfoList = append(requestBody.ReqInfo.SubnetInfoList, spiderSubnetInfo)
	}

	client := resty.New()
	method := "POST"
	var callResult SpiderVPCInfo
	var url string

	if option == "register" && u.CspVNetId == "" {
		url = fmt.Sprintf("%s/vpc/%s", common.SpiderRestUrl, u.Name)
		method = "GET"
	} else if option == "register" && u.CspVNetId != "" {
		url = fmt.Sprintf("%s/regvpc", common.SpiderRestUrl)
	} else { // option != "register"
		url = fmt.Sprintf("%s/vpc", common.SpiderRestUrl)
	}

	err = common.ExecuteHttpRequest(
		client,
		method,
		url,
		nil,
		common.SetUseBody(requestBody),
		&requestBody,
		&callResult,
		common.MediumDuration,
	)
	if err != nil {
		log.Error().Err(err).Msg("")
		return temp, err
	}

	content := TbVNetInfo{}
	//content.Id = common.GenUid()
	content.Id = u.Name
	content.Name = u.Name
	content.ConnectionName = u.ConnectionName
	content.CspVNetId = callResult.IId.SystemId
	content.CspVNetName = callResult.IId.NameId
	content.CidrBlock = callResult.IPv4_CIDR
	content.Description = u.Description
	content.KeyValueList = callResult.KeyValueList
	content.AssociatedObjectList = []string{}

	if option == "register" && u.CspVNetId == "" {
		content.SystemLabel = "Registered from CB-Spider resource"
	} else if option == "register" && u.CspVNetId != "" {
		content.SystemLabel = "Registered from CSP resource"
	}

	// cb-store
	Key := common.GenResourceKey(nsId, common.StrVNet, content.Id)
	Val, _ := json.Marshal(content)

	err = common.CBStore.Put(Key, string(Val))
	if err != nil {
		log.Error().Err(err).Msg("")
		return content, err
	}

	for _, v := range callResult.SubnetInfoList {
		jsonBody, err := json.Marshal(v)
		if err != nil {
			log.Error().Err(err).Msg("")
		}

		tbSubnetReq := TbSubnetReq{}
		err = json.Unmarshal(jsonBody, &tbSubnetReq)
		if err != nil {
			log.Error().Err(err).Msg("")
		}
		tbSubnetReq.Name = v.IId.NameId

		_, err = CreateSubnet(nsId, content.Id, tbSubnetReq, true)
		if err != nil {
			log.Error().Err(err).Msg("")
		}
	}

	keyValue, err := common.CBStore.Get(Key)
	if err != nil {
		log.Error().Err(err).Msg("")
		err = fmt.Errorf("In CreateVNet(); CBStore.Get() returned an error.")
		log.Error().Err(err).Msg("")
		// return nil, err
	}

	result := TbVNetInfo{}
	err = json.Unmarshal([]byte(keyValue.Value), &result)
	if err != nil {
		log.Error().Err(err).Msg("")
	}
	return result, nil
}
