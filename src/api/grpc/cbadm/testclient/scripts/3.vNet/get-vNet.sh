#!/bin/bash

#function get_vNet() {


	TestSetFile=${4:-../testSet.env}
    if [ ! -f "$TestSetFile" ]; then
        echo "$TestSetFile does not exist."
        exit
    fi
	source $TestSetFile
    source ../conf.env
	
	echo "####################################################################"
	echo "## 3. vNet: Get"
	echo "####################################################################"

	CSP=${1}
	REGION=${2:-1}
	POSTFIX=${3:-developer}

	source ../common-functions.sh
	getCloudIndex $CSP

    $CBTUMBLEBUG_ROOT/src/api/grpc/cbadm/cbadm network get --config $CBTUMBLEBUG_ROOT/src/api/grpc/cbadm/grpc_conf.yaml -o json --ns $NSID --id ${CONN_CONFIG[$INDEX,$REGION]}-${POSTFIX} | jq ''
    echo ""
#}

#get_vNet