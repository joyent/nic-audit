/*
 * Copyright (c) 2017, Joyent, Inc. All rights reserved.
 *
 * This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/.
 */
package main

import (
	"container/list"
	"context"
	"io/ioutil"
	"log"
	"net"
)

import (
	triton "github.com/joyent/triton-go"
	"github.com/joyent/triton-go/authentication"
	"github.com/joyent/triton-go/compute"
	"github.com/twinj/uuid"
)

// auditAccount is the main function that kicks off the process where
// every account is audited for offending network combinations.
func auditAccount(account Account, nicGroups map[string][]string, config Configuration) error {
	log.Printf("%v\n", account)

	client, clientErr := setupTritonClient(account)

	if clientErr != nil {
		return clientErr
	}

	listInput := &compute.ListInstancesInput{}
	instances, instancesErr := client.Instances().List(context.Background(), listInput)

	if instancesErr != nil {
		return instancesErr
	}

	alerts := createAlertsForOffendingNetworks(account, instances, nicGroups)
	processAlerts(alerts, *client, config)

	return nil
}

// setupTritonClient configures and instantiates a Triton client that
// allows you to programmatically access the Triton CloudAPI.
func setupTritonClient(account Account) (*compute.ComputeClient, error) {
	privateKey, privateKeyReadErr := ioutil.ReadFile(account.KeyPath)

	if privateKeyReadErr != nil {
		return &compute.ComputeClient{}, privateKeyReadErr
	}

	sshKeySigner, signerErr := authentication.NewPrivateKeySigner(
		account.KeyId, privateKey, account.AccountName)

	if signerErr != nil {
		return &compute.ComputeClient{}, signerErr
	}

	config := &triton.ClientConfig{
		TritonURL:   account.TritonUrl,
		AccountName: account.AccountName,
		Signers:     []authentication.Signer{sshKeySigner},
	}

	return compute.NewClient(config)
}

// createAlertsForOffendingNetworks aggregates alerts for every offending
// network pattern match and returns the results as a list.
func createAlertsForOffendingNetworks(account Account, instances []*compute.Instance,
	nicGroups map[string][]string) list.List {

	alerts := list.New()

	for _, instance := range instances {
		for nicGroup, networkIds := range nicGroups {
			matchingTotal := countMatchingNetworkIds(*instance, networkIds)

			if matchingTotal == len(networkIds) {
				alert := Alert{
					Instance:     *instance,
					Account:      account,
					NicGroupName: nicGroup,
					NicGroupIds:  networkIds,
				}
				alerts.PushBack(alert)
			}
		}
	}

	return *alerts
}

// countMatchingNetworkIds counts the number of networks that matched the
// offending network match criteria. Typically, the result of this method
// would be compared to the number of offending networks in the nib_groups
// configuration.
func countMatchingNetworkIds(instance compute.Instance, searchStrings []string) int {
	count := 0

	for _, search := range searchStrings {
		// If our search string is another UUID it is a simple match
		_, uuidErr := uuid.Parse(search)
		if uuidErr == nil {
			for _, id := range instance.Networks {
				if id == search {
					count += 1
				}
			}
			continue
		}

		// If our search string is a CIDR
		_, ipNet, ipErr := net.ParseCIDR(search)
		if ipErr == nil {
			for _, instanceIpString := range instance.IPs {
				instanceIp := net.ParseIP(instanceIpString)
				if ipNet.Contains(instanceIp) {
					count += 1
				}
			}
			continue
		}

		if search == "public" {
			for _, instanceIpString := range instance.IPs {
				instanceIp := net.ParseIP(instanceIpString)
				if isPublicIP(instanceIp) {
					count += 1
				}
			}
			continue
		}

		log.Fatalf("Invalid nic_group search string: %v\n", search)
	}
	return count
}