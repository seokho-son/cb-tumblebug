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
	"reflect"
	"strconv"
	"strings"

	"github.com/cloud-barista/cb-tumblebug/src/core/common"
	"github.com/cloud-barista/cb-tumblebug/src/kvstore/kvstore"
	validator "github.com/go-playground/validator/v10"
	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
)

// CreateFirewallRules accepts firewallRule creation request, creates and returns an TB securityGroup object
func CreateFirewallRules(nsId string, securityGroupId string, req []TbFirewallRuleInfo, objectOnly bool) (TbSecurityGroupInfo, error) {
	// Which one would be better, 'req TbFirewallRuleInfo' vs. 'req TbFirewallRuleInfo' ?

	err := common.CheckString(nsId)
	if err != nil {
		temp := TbSecurityGroupInfo{}
		log.Error().Err(err).Msg("")
		return temp, err
	}

	err = common.CheckString(securityGroupId)
	if err != nil {
		temp := TbSecurityGroupInfo{}
		log.Error().Err(err).Msg("")
		return temp, err
	}

	// Validate each TbFirewallRule
	for i, v := range req {
		err = validate.Struct(v)
		if err != nil {

			if _, ok := err.(*validator.InvalidValidationError); ok {
				log.Err(err).Msg("")
				temp := TbSecurityGroupInfo{}
				return temp, err
			}

			temp := TbSecurityGroupInfo{}
			return temp, err
		}

		req[i].IPProtocol = strings.ToUpper(req[i].IPProtocol)
		req[i].Direction = strings.ToLower(req[i].Direction)
	}

	parentResourceType := common.StrSecurityGroup

	check, err := CheckResource(nsId, parentResourceType, securityGroupId)

	if !check {
		temp := TbSecurityGroupInfo{}
		err := fmt.Errorf("The securityGroup %s does not exist.", securityGroupId)
		return temp, err
	}

	if err != nil {
		temp := TbSecurityGroupInfo{}
		err := fmt.Errorf("Failed to check the existence of the securityGroup %s.", securityGroupId)
		return temp, err
	}

	securityGroupKey := common.GenResourceKey(nsId, common.StrSecurityGroup, securityGroupId)
	securityGroupKeyValue, _ := kvstore.GetKv(securityGroupKey)
	oldSecurityGroup := TbSecurityGroupInfo{}
	err = json.Unmarshal([]byte(securityGroupKeyValue.Value), &oldSecurityGroup)
	if err != nil {
		log.Error().Err(err).Msg("")
		return oldSecurityGroup, err
	}

	// Return error if the exactly same rule already exists.
	oldSGsFirewallRules := oldSecurityGroup.FirewallRules

	for _, oldRule := range oldSGsFirewallRules {

		for _, newRule := range req {
			if reflect.DeepEqual(oldRule, newRule) {
				err := fmt.Errorf("One of submitted firewall rules already exists in the SG %s.", securityGroupId)
				return oldSecurityGroup, err
			}
		}
	}

	var tempSpiderSecurityInfo *SpiderSecurityInfo

	if objectOnly == false { // then, call CB-Spider CreateSecurityRule API
		requestBody := SpiderSecurityRuleReqInfoWrapper{}
		requestBody.ConnectionName = oldSecurityGroup.ConnectionName
		for _, newRule := range req {
			requestBody.ReqInfo.RuleInfoList = append(requestBody.ReqInfo.RuleInfoList, SpiderSecurityRuleInfo(newRule)) // Is this really works?
		}

		url := fmt.Sprintf("%s/securitygroup/%s/rules", common.SpiderRestUrl, oldSecurityGroup.CspSecurityGroupName)

		client := resty.New().SetCloseConnection(true)

		resp, err := client.R().
			SetHeader("Content-Type", "application/json").
			SetBody(requestBody).
			SetResult(&SpiderSecurityInfo{}). // or SetResult(AuthSuccess{}).
			//SetError(&AuthError{}).       // or SetError(AuthError{}).
			Post(url)

		if err != nil {
			log.Error().Err(err).Msg("")
			content := TbSecurityGroupInfo{}
			err := fmt.Errorf("an error occurred while requesting to CB-Spider")
			return content, err
		}

		fmt.Println("HTTP Status code: " + strconv.Itoa(resp.StatusCode()))
		switch {
		case resp.StatusCode() >= 400 || resp.StatusCode() < 200:
			err := fmt.Errorf(string(resp.Body()))
			log.Error().Err(err).Msg("")
			content := TbSecurityGroupInfo{}
			return content, err
		}

		tempSpiderSecurityInfo = resp.Result().(*SpiderSecurityInfo)

	}

	log.Info().Msg("POST CreateFirewallRule")

	newSecurityGroup := TbSecurityGroupInfo{}
	newSecurityGroup = oldSecurityGroup
	newSecurityGroup.FirewallRules = nil

	for _, newSpiderSecurityRule := range tempSpiderSecurityInfo.SecurityRules {
		newSecurityGroup.FirewallRules = append(newSecurityGroup.FirewallRules, TbFirewallRuleInfo(newSpiderSecurityRule))
	}
	Val, _ := json.Marshal(newSecurityGroup)

	err = kvstore.Put(securityGroupKey, string(Val))
	if err != nil {
		log.Error().Err(err).Msg("")
		return oldSecurityGroup, err
	}

	// securityGroupKey := common.GenResourceKey(nsId, common.StrSecurityGroup, securityGroupId)
	// keyValue, _ := kvstore.GetKv(securityGroupKey)
	//
	//
	// content := TbSecurityGroupInfo{}
	// err = json.Unmarshal([]byte(keyValue.Value), &content)
	// if err != nil {
	// 	log.Error().Err(err).Msg("")
	// 	return err
	// }
	return newSecurityGroup, nil
}

// DeleteFirewallRules accepts firewallRule creation request, creates and returns an TB securityGroup object
func DeleteFirewallRules(nsId string, securityGroupId string, req []TbFirewallRuleInfo) (TbSecurityGroupInfo, error) {

	err := common.CheckString(nsId)
	if err != nil {
		temp := TbSecurityGroupInfo{}
		log.Error().Err(err).Msg("")
		return temp, err
	}

	err = common.CheckString(securityGroupId)
	if err != nil {
		temp := TbSecurityGroupInfo{}
		log.Error().Err(err).Msg("")
		return temp, err
	}

	// Validate each TbFirewallRule
	for i, v := range req {
		err = validate.Struct(v)
		if err != nil {

			if _, ok := err.(*validator.InvalidValidationError); ok {
				log.Err(err).Msg("")
				temp := TbSecurityGroupInfo{}
				return temp, err
			}

			temp := TbSecurityGroupInfo{}
			return temp, err
		}

		req[i].IPProtocol = strings.ToUpper(req[i].IPProtocol)
		req[i].Direction = strings.ToLower(req[i].Direction)
	}

	parentResourceType := common.StrSecurityGroup

	check, err := CheckResource(nsId, parentResourceType, securityGroupId)

	if !check {
		temp := TbSecurityGroupInfo{}
		err := fmt.Errorf("The securityGroup %s does not exist.", securityGroupId)
		return temp, err
	}

	if err != nil {
		temp := TbSecurityGroupInfo{}
		err := fmt.Errorf("Failed to check the existence of the securityGroup %s.", securityGroupId)
		return temp, err
	}

	securityGroupKey := common.GenResourceKey(nsId, common.StrSecurityGroup, securityGroupId)
	securityGroupKeyValue, _ := kvstore.GetKv(securityGroupKey)
	oldSecurityGroup := TbSecurityGroupInfo{}
	err = json.Unmarshal([]byte(securityGroupKeyValue.Value), &oldSecurityGroup)
	if err != nil {
		log.Error().Err(err).Msg("")
		return oldSecurityGroup, err
	}

	// Return error if one or more of provided rules does not exist.
	oldSGsFirewallRules := oldSecurityGroup.FirewallRules

	found_flag := false

	rulesToDelete := []TbFirewallRuleInfo{}

	for _, oldRule := range oldSGsFirewallRules {

		for _, newRule := range req {
			if reflect.DeepEqual(oldRule, newRule) {
				found_flag = true
				rulesToDelete = append(rulesToDelete, newRule)
				continue
			}
		}
	}

	type SpiderDeleteSecurityRulesResp struct {
		Result string
	}

	var spiderDeleteSecurityRulesResp *SpiderDeleteSecurityRulesResp

	requestBody := SpiderSecurityRuleReqInfoWrapper{}
	requestBody.ConnectionName = oldSecurityGroup.ConnectionName

	if found_flag == false {
		err := fmt.Errorf("Any of submitted firewall rules does not exist in the SG %s.", securityGroupId)
		log.Error().Err(err).Msg("")
		return oldSecurityGroup, err
	} else {
		for _, v := range rulesToDelete {
			requestBody.ReqInfo.RuleInfoList = append(requestBody.ReqInfo.RuleInfoList, SpiderSecurityRuleInfo(v))
		}
	}

	url := fmt.Sprintf("%s/securitygroup/%s/rules", common.SpiderRestUrl, oldSecurityGroup.CspSecurityGroupName)

	client := resty.New().SetCloseConnection(true)

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(requestBody).
		SetResult(&SpiderDeleteSecurityRulesResp{}). // or SetResult(AuthSuccess{}).
		//SetError(&AuthError{}).       // or SetError(AuthError{}).
		Delete(url)

	if err != nil {
		log.Error().Err(err).Msg("")
		content := TbSecurityGroupInfo{}
		err := fmt.Errorf("an error occurred while requesting to CB-Spider")
		return content, err
	}

	fmt.Println("HTTP Status code: " + strconv.Itoa(resp.StatusCode()))
	switch {
	case resp.StatusCode() >= 400 || resp.StatusCode() < 200:
		err := fmt.Errorf(string(resp.Body()))
		log.Error().Err(err).Msg("")
		content := TbSecurityGroupInfo{}
		return content, err
	}

	spiderDeleteSecurityRulesResp = resp.Result().(*SpiderDeleteSecurityRulesResp)

	if spiderDeleteSecurityRulesResp.Result != "true" {
		err := fmt.Errorf("Failed to delete Security Group rules with CB-Spider.")
		log.Error().Err(err).Msg("")
		return oldSecurityGroup, err
	}

	requestBody2 := common.SpiderConnectionName{}
	requestBody2.ConnectionName = oldSecurityGroup.ConnectionName

	var tempSpiderSecurityInfo *SpiderSecurityInfo

	url = fmt.Sprintf("%s/securitygroup/%s", common.SpiderRestUrl, oldSecurityGroup.CspSecurityGroupName)

	client = resty.New().SetCloseConnection(true)
	client.SetAllowGetMethodPayload(true)

	resp, err = client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(requestBody2).
		SetResult(&SpiderSecurityInfo{}). // or SetResult(AuthSuccess{}).
		//SetError(&AuthError{}).       // or SetError(AuthError{}).
		Get(url)

	if err != nil {
		log.Error().Err(err).Msg("")
		content := TbSecurityGroupInfo{}
		err := fmt.Errorf("an error occurred while requesting to CB-Spider")
		return content, err
	}

	fmt.Println("HTTP Status code: " + strconv.Itoa(resp.StatusCode()))
	switch {
	case resp.StatusCode() >= 400 || resp.StatusCode() < 200:
		err := fmt.Errorf(string(resp.Body()))
		log.Error().Err(err).Msg("")
		content := TbSecurityGroupInfo{}
		return content, err
	}

	tempSpiderSecurityInfo = resp.Result().(*SpiderSecurityInfo)

	log.Info().Msg("DELETE FirewallRule")

	newSecurityGroup := TbSecurityGroupInfo{}
	newSecurityGroup = oldSecurityGroup
	newSecurityGroup.FirewallRules = nil
	for _, newSpiderSecurityRule := range tempSpiderSecurityInfo.SecurityRules {
		newSecurityGroup.FirewallRules = append(newSecurityGroup.FirewallRules, TbFirewallRuleInfo(newSpiderSecurityRule))
	}
	Val, _ := json.Marshal(newSecurityGroup)

	err = kvstore.Put(securityGroupKey, string(Val))
	if err != nil {
		log.Error().Err(err).Msg("")
		return oldSecurityGroup, err
	}

	// securityGroupKey := common.GenResourceKey(nsId, common.StrSecurityGroup, securityGroupId)
	// keyValue, _ := kvstore.GetKv(securityGroupKey)
	//
	//
	// content := TbSecurityGroupInfo{}
	// err = json.Unmarshal([]byte(keyValue.Value), &content)
	// if err != nil {
	// 	log.Error().Err(err).Msg("")
	// 	return err
	// }
	return newSecurityGroup, nil
}
