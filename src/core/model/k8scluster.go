/*
Copyright 2023 The Cloud-Barista Authors.
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

// Package model is to handle object of CB-Tumblebug
package model

import (
	"time"
)

// 2023-11-13 https://github.com/cloud-barista/cb-spider/blob/fa4bd91fdaa6bb853ea96eca4a7b4f58a2abebf2/cloud-control-manager/cloud-driver/interfaces/resources/ClusterHandler.go#L1

/*
TODO: Implement Register/Unregister

// SpiderClusterRegisterReqInfoWrapper is a wrapper struct to create JSON body of 'Register Cluster request'
type SpiderClusterRegisterReqInfoWrapper struct {
	ConnectionName string
	ReqInfo        SpiderClusterRegisterReqInfo
}

// 2023-11-13 https://github.com/cloud-barista/cb-spider/blob/fa4bd91fdaa6bb853ea96eca4a7b4f58a2abebf2/api-runtime/rest-runtime/ClusterRest.go#L52

// SpiderClusterRegisterReqInfo is a struct to create JSON body of 'Register Cluster request'
type SpiderClusterRegisterReqInfo struct {
	VPCName string
	Name    string
	CSPId   string
}

// 2023-11-13 https://github.com/cloud-barista/cb-spider/blob/fa4bd91fdaa6bb853ea96eca4a7b4f58a2abebf2/api-runtime/rest-runtime/ClusterRest.go#L86

// SpiderClusterUnregisterReqInfoWrapper is a wrapper struct to create JSON body of 'Unregister Cluster request'
type SpiderClusterUnregisterReqInfoWrapper struct {
	ConnectionName string
}
*/

/*
 * K8sCluster Request
 */

// 2023-11-13 https://github.com/cloud-barista/cb-spider/blob/fa4bd91fdaa6bb853ea96eca4a7b4f58a2abebf2/cloud-control-manager/cloud-driver/interfaces/resources/ClusterHandler.go#L1

// SpiderClusterReq is a wrapper struct to create JSON body of 'Create Cluster request'
type SpiderClusterReq struct {
	NameSpace      string // should be empty string from Tumblebug
	ConnectionName string
	ReqInfo        SpiderClusterReqInfo
}

// 2023-11-13 https://github.com/cloud-barista/cb-spider/blob/fa4bd91fdaa6bb853ea96eca4a7b4f58a2abebf2/api-runtime/rest-runtime/ClusterRest.go#L110

// SpiderClusterReqInfo is a struct to create JSON body of 'Create Cluster request'
type SpiderClusterReqInfo struct {
	// (1) Cluster Info
	Name    string
	Version string

	// (2) Network Info
	VPCName            string
	SubnetNames        []string
	SecurityGroupNames []string

	// (3) NodeGroupInfo List
	NodeGroupList []SpiderNodeGroupReqInfo
}

// TbK8sClusterReq is a struct to handle 'Create K8sCluster' request toward CB-Tumblebug.
type TbK8sClusterReq struct { // Tumblebug
	//Namespace      string `json:"namespace" validate:"required" example:"default"`
	ConnectionName string `json:"connectionName" validate:"required" example:"alibaba-ap-northeast-2"`
	Description    string `json:"description" example:"My K8sCluster"`

	// (1) K8sCluster Info
	Name    string `json:"name" validate:"required" example:"k8scluster-01"`
	Version string `json:"version" example:"1.30.1-aliyun.1"`

	// (2) Network Info
	VNetId           string   `json:"vNetId" validate:"required" example:"vpc-01"`
	SubnetIds        []string `json:"subnetIds" validate:"required" example:"subnet-01"`
	SecurityGroupIds []string `json:"securityGroupIds" validate:"required" example:"sg-01"`

	// (3) NodeGroupInfo List
	K8sNodeGroupList []TbK8sNodeGroupReq `json:"k8sNodeGroupList"`

	// Fields for "Register existing K8sCluster" feature
	// @description CspResourceId is required to register a k8s cluster from CSP (option=register)
	CspResourceId string `json:"cspResourceId" example:"required when option is register"`
}

// 2023-11-13 https://github.com/cloud-barista/cb-spider/blob/fa4bd91fdaa6bb853ea96eca4a7b4f58a2abebf2/api-runtime/rest-runtime/ClusterRest.go#L441

// SpiderNodeGroupReq is a wrapper struct to create JSON body of 'Add NodeGroup' request
type SpiderNodeGroupReq struct {
	NameSpace      string // should be empty string from Tumblebug
	ConnectionName string
	ReqInfo        SpiderNodeGroupReqInfo
}

// SpiderNodeGroupReqInfo is a wrapper struct to create JSON body of 'Add NodeGroup' request
type SpiderNodeGroupReqInfo struct {
	Name         string
	ImageName    string
	VMSpecName   string
	RootDiskType string
	RootDiskSize string
	KeyPairName  string

	// autoscale config.
	OnAutoScaling   string
	DesiredNodeSize string
	MinNodeSize     string
	MaxNodeSize     string
}

// TbK8sNodeGroupReq is a struct to handle requests related to K8sNodeGroup toward CB-Tumblebug.
type TbK8sNodeGroupReq struct {
	Name         string `json:"name" example:"ng-01"`
	CspImageName string `json:"cspImageName" example:"image-01"`
	CspSpecName  string `json:"cspSpecName" example:"Standard_B2s (temporarily, CSP's Spec Names are valid. It will be upgraded)"`
	RootDiskType string `json:"rootDiskType" example:"cloud_essd" enum:"default, TYPE1, ..."` // "", "default", "TYPE1", AWS: ["standard", "gp2", "gp3"], Azure: ["PremiumSSD", "StandardSSD", "StandardHDD"], GCP: ["pd-standard", "pd-balanced", "pd-ssd", "pd-extreme"], ALIBABA: ["cloud_efficiency", "cloud", "cloud_ssd"], TENCENT: ["CLOUD_PREMIUM", "CLOUD_SSD"]
	RootDiskSize string `json:"rootDiskSize" example:"40" enum:"default, 30, 42, ..."`        // "default", Integer (GB): ["50", ..., "1000"]
	SshKeyId     string `json:"sshKeyId" example:"sshkey-01"`

	// autoscale config.
	OnAutoScaling   string `json:"onAutoScaling" example:"true"`
	DesiredNodeSize string `json:"desiredNodeSize" example:"1"`
	MinNodeSize     string `json:"minNodeSize" example:"1"`
	MaxNodeSize     string `json:"maxNodeSize" example:"3"`
}

// SpiderSetAutoscalingReq is a wrapper struct to create JSON body of 'Set Autoscaling On/Off' request.
type SpiderSetAutoscalingReq struct {
	ConnectionName string
	ReqInfo        SpiderSetAutoscalingReqInfo
}

// SpiderSetAutoscalingReqInfo is a wrapper struct to create JSON body of 'Set Autoscaling On/Off' request.
type SpiderSetAutoscalingReqInfo struct {
	OnAutoScaling string
}

// SpiderSetAutoscalingRes is a wrapper struct to create JSON body of 'Set Autoscaling On/Off' response.
type SpiderSetAutoscalingRes struct {
	Result string
}

// TbSetK8sNodeGroupAutoscalingReq is a struct to handle 'Set K8sNodeGroup's Autoscaling' request toward CB-Tumblebug.
type TbSetK8sNodeGroupAutoscalingReq struct {
	OnAutoScaling string `json:"onAutoScaling" example:"true"`
}

// TbSetK8sNodeGroupAutoscalingRes is a struct to handle 'Set K8sNodeGroup's Autoscaling' response from CB-Tumblebug.
type TbSetK8sNodeGroupAutoscalingRes struct {
	Result string `json:"result" example:"true"`
}

// SpiderChangeAutoscaleSizeReq is a wrapper struct to create JSON body of 'Change Autoscale Size' request.
type SpiderChangeAutoscaleSizeReq struct {
	ConnectionName string
	ReqInfo        SpiderChangeAutoscaleSizeReqInfo
}

// SpiderChangeAutoscaleSizeReqInfo is a wrapper struct to create JSON body of 'Change Autoscale Size' request.
type SpiderChangeAutoscaleSizeReqInfo struct {
	DesiredNodeSize string
	MinNodeSize     string
	MaxNodeSize     string
}

// TbChangeK8sNodeGroupAutoscaleSizeReq is a struct to handle 'Change K8sNodeGroup's Autoscale Size' request toward CB-Tumblebug.
type TbChangeK8sNodeGroupAutoscaleSizeReq struct {
	DesiredNodeSize string `json:"desiredNodeSize" example:"1"`
	MinNodeSize     string `json:"minNodeSize" example:"1"`
	MaxNodeSize     string `json:"maxNodeSize" example:"3"`
}

// SpiderChangeAutoscaleSizeRes is a wrapper struct to get JSON body of 'Change Autoscale Size' response
type SpiderChangeAutoscaleSizeRes struct {
	ConnectionName string
	NodeGroupInfo  SpiderNodeGroupInfo
}

// TbChangeK8sNodeGroupAutoscaleSizeRes is a struct to handle 'Change K8sNodeGroup's Autoscale Size' response from CB-Tumblebug.
type TbChangeK8sNodeGroupAutoscaleSizeRes struct {
	TbK8sNodeGroupInfo
}

// SpiderUpgradeClusterReq is a wrapper struct to create JSON body of 'Upgrade Cluster' request
type SpiderUpgradeClusterReq struct {
	NameSpace      string // should be empty string from Tumblebug
	ConnectionName string
	ReqInfo        SpiderUpgradeClusterReqInfo
}

// SpiderUpgradeClusterReqInfo is a wrapper struct to create JSON body of 'Upgrade Cluster' request
type SpiderUpgradeClusterReqInfo struct {
	Version string
}

// TbUpgradeK8sClusterReq is a struct to handle 'Upgrade K8sCluster' request toward CB-Tumblebug.
type TbUpgradeK8sClusterReq struct {
	Version string `json:"version" example:"1.30.1-alyun.1"`
}

/*
 * Cluster Const
 */

// 2023-11-14 https://github.com/cloud-barista/cb-spider/blob/fa4bd91fdaa6bb853ea96eca4a7b4f58a2abebf2/cloud-control-manager/cloud-driver/interfaces/resources/ClusterHandler.go#L15

type SpiderClusterStatus string

const (
	SpiderClusterCreating SpiderClusterStatus = "Creating"
	SpiderClusterActive   SpiderClusterStatus = "Active"
	SpiderClusterInactive SpiderClusterStatus = "Inactive"
	SpiderClusterUpdating SpiderClusterStatus = "Updating"
	SpiderClusterDeleting SpiderClusterStatus = "Deleting"
)

type SpiderNodeGroupStatus string

const (
	SpiderNodeGroupCreating SpiderNodeGroupStatus = "Creating"
	SpiderNodeGroupActive   SpiderNodeGroupStatus = "Active"
	SpiderNodeGroupInactive SpiderNodeGroupStatus = "Inactive"
	SpiderNodeGroupUpdating SpiderNodeGroupStatus = "Updating"
	SpiderNodeGroupDeleting SpiderNodeGroupStatus = "Deleting"
)

type TbK8sClusterStatus string

const (
	TbK8sClusterCreating TbK8sClusterStatus = "Creating"
	TbK8sClusterActive   TbK8sClusterStatus = "Active"
	TbK8sClusterInactive TbK8sClusterStatus = "Inactive"
	TbK8sClusterUpdating TbK8sClusterStatus = "Updating"
	TbK8sClusterDeleting TbK8sClusterStatus = "Deleting"
)

type TbK8sNodeGroupStatus string

const (
	TbK8sNodeGroupCreating TbK8sNodeGroupStatus = "Creating"
	TbK8sNodeGroupActive   TbK8sNodeGroupStatus = "Active"
	TbK8sNodeGroupInactive TbK8sNodeGroupStatus = "Inactive"
	TbK8sNodeGroupUpdating TbK8sNodeGroupStatus = "Updating"
	TbK8sNodeGroupDeleting TbK8sNodeGroupStatus = "Deleting"
)

/*
 * Cluster Info Structure
 */

// 2023-11-14 https://github.com/cloud-barista/cb-spider/blob/fa4bd91fdaa6bb853ea96eca4a7b4f58a2abebf2/cloud-control-manager/cloud-driver/interfaces/resources/ClusterHandler.go#L37

// SpiderClusterRes is a wrapper struct to handle a Cluster information from the CB-Spider's REST API response
type SpiderClusterRes struct {
	ConnectionName string
	ClusterInfo    SpiderClusterInfo
}

// SpiderClusterInfo is a struct to handle Cluster information from the CB-Spider's REST API response
type SpiderClusterInfo struct {
	IId IID // {NameId, SystemId}

	Version string // Kubernetes Version, ex) 1.23.3
	Network SpiderNetworkInfo

	// ---

	NodeGroupList []SpiderNodeGroupInfo
	AccessInfo    SpiderAccessInfo
	Addons        SpiderAddonsInfo

	Status SpiderClusterStatus

	CreatedTime  time.Time
	KeyValueList []KeyValue
}

// TbK8sClusterInfo is a struct that represents TB K8sCluster object.
type TbK8sClusterInfo struct {
	// ResourceType is the type of the resource
	ResourceType string `json:"resourceType"`

	// Id is unique identifier for the object
	Id string `json:"id" example:"aws-ap-southeast-1"`
	// Uid is universally unique identifier for the object, used for labelSelector
	Uid string `json:"uid,omitempty" example:"wef12awefadf1221edcf"`
	// CspResourceName is name assigned to the CSP resource. This name is internally used to handle the resource.
	CspResourceName string `json:"cspResourceName,omitempty" example:"we12fawefadf1221edcf"`
	// CspResourceId is resource identifier managed by CSP
	CspResourceId string `json:"cspResourceId,omitempty" example:"csp-06eb41e14121c550a"`

	// Name is human-readable string to represent the object
	Name           string `json:"name" example:"aws-ap-southeast-1"`
	ConnectionName string `json:"connectionName" example:"alibaba-ap-northeast-2"`

	Version string `json:"version" example:"1.30.1-aliyun.1"` // Kubernetes Version, ex) 1.23.3
	Network TbK8sClusterNetworkInfo

	// ---

	K8sNodeGroupList []TbK8sNodeGroupInfo
	AccessInfo       TbK8sAccessInfo
	Addons           TbK8sAddonsInfo

	Status TbK8sClusterStatus `json:"status" example:"Creating"` // Creating, Active, Inactive, Updating, Deleting

	CreatedTime  time.Time  `json:"createdTime" example:"1970-01-01T00:00:00.00Z"`
	KeyValueList []KeyValue `json:"keyValueList"`

	Description string `json:"description" example:"My K8sCluster"`

	// Latest system message such as error message
	SystemMessage string `json:"systemMessage" example:"Failed because ..." default:""` // systeam-given string message

	// SystemLabel is for describing the Resource in a keyword (any string can be used) for special System purpose
	SystemLabel string `json:"systemLabel" example:"Managed by CB-Tumblebug" default:""`

	//CspViewK8sClusterDetail SpiderClusterInfo `json:cspViewK8sClusterDetail,omitempty"`
}

// SpiderNetworkInfo is a struct to handle Cluster Network information from the CB-Spider's REST API response
type SpiderNetworkInfo struct {
	VpcIID            IID // {NameId, SystemId}
	SubnetIIDs        []IID
	SecurityGroupIIDs []IID

	// ---

	KeyValueList []KeyValue
}

// TbK8sClusterNetworkInfo is a struct to handle K8sCluster Network information from the CB-Tumblebug's REST API response
type TbK8sClusterNetworkInfo struct {
	VNetId           string   `json:"vNetId" example:"vpc-01"`
	SubnetIds        []string `json:"subnetIds" example:"subnet-01"`
	SecurityGroupIds []string `json:"securityGroupIds" example:"sg-01"`

	// ---

	KeyValueList []KeyValue `json:"keyValueList"`
}

// SpiderNodeGroupInfo is a struct to handle Cluster Node Group information from the CB-Spider's REST API response
type SpiderNodeGroupInfo struct {
	IId IID // {NameId, SystemId}

	// VM config.
	ImageIID     IID
	VMSpecName   string
	RootDiskType string // "SSD(gp2)", "Premium SSD", ...
	RootDiskSize string // "", "default", "50", "1000" (GB)
	KeyPairIID   IID

	// Scaling config.
	OnAutoScaling   bool // default: true
	DesiredNodeSize int
	MinNodeSize     int
	MaxNodeSize     int

	// ---

	Status SpiderNodeGroupStatus
	Nodes  []IID

	KeyValueList []KeyValue
}

// TbK8sNodeGroupInfo is a struct to handle K8sCluster's Node Group information from the CB-Tumblebug's REST API response
type TbK8sNodeGroupInfo struct {
	// Id is unique identifier for the object
	Id string `json:"id" example:"aws-ap-southeast-1"`
	// Uid is universally unique identifier for the object, used for labelSelector
	Uid string `json:"uid,omitempty" example:"wef12awefadf1221edcf"`
	// CspResourceName is name assigned to the CSP resource. This name is internally used to handle the resource.
	CspResourceName string `json:"cspResourceName,omitempty" example:"we12fawefadf1221edcf"`
	// CspResourceId is resource identifier managed by CSP
	CspResourceId string `json:"cspResourceId,omitempty" example:"csp-06eb41e14121c550a"`

	// Name is human-readable string to represent the object
	Name string `json:"name" example:"aws-ap-southeast-1"`

	// VM config.
	CspImageName string `json:"cspImageName" example:"image-01"`
	CspSpecName  string `json:"cspSpecName" example:"spec-01"`
	RootDiskType string `json:"rootDiskType" example:"cloud_essd"`
	RootDiskSize string `json:"rootDiskSize" example:"40"`
	SshKeyId     string `json:"sshKeyId" example:"sshkey-01"`

	// Scaling config.
	OnAutoScaling   bool `json:"onAutoScaling" example:"true"`
	DesiredNodeSize int  `json:"desiredNodeSize" example:"1"`
	MinNodeSize     int  `json:"minNodeSize" example:"1"`
	MaxNodeSize     int  `json:"maxNodeSize" example:"3"`

	// ---
	Status   TbK8sNodeGroupStatus `json:"status" example:"Creating"`  // Creating, Active, Inactive, Updating, Deleting
	K8sNodes []string             `json:"k8sNodes" example:"node-01"` // id for nodes

	KeyValueList []KeyValue `json:"keyValueList"`
}

// SpiderAccessInfo is a struct to handle Cluster Access information from the CB-Spider's REST API response
type SpiderAccessInfo struct {
	Endpoint   string // ex) https://1.2.3.4:6443
	Kubeconfig string
}

// TbK8sAccessInfo is a struct to handle K8sCluster Access information from the CB-Tumblebug's REST API response
type TbK8sAccessInfo struct {
	Endpoint   string `json:"endpoint" example:"http://1.2.3.4:6443"`
	Kubeconfig string `json:"kubeconfig" example:"apiVersion: v1\nclusters:\n- cluster:\n certificate-authority-data: LS0..."`
}

// SpiderAddonsInfo is a struct to handle Cluster Addons information from the CB-Spider's REST API response
type SpiderAddonsInfo struct {
	KeyValueList []KeyValue
}

// TbK8sAddonsInfo is a struct to handle K8sCluster Addons information from the CB-Tumblebug's REST API response
type TbK8sAddonsInfo struct {
	KeyValueList []KeyValue `json:"keyValueList"`
}