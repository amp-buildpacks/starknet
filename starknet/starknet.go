// Copyright (c) The Amphitheatre Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package starknet

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/buildpacks/libcnb"
	"github.com/paketo-buildpacks/libpak"
	"github.com/paketo-buildpacks/libpak/bard"
	"github.com/paketo-buildpacks/libpak/crush"
	"github.com/paketo-buildpacks/libpak/effect"
	"github.com/paketo-buildpacks/libpak/sherpa"
)

type Starknet struct {
	LayerContributor libpak.DependencyLayerContributor
	configResolver   libpak.ConfigurationResolver
	Logger           bard.Logger
	Executor         effect.Executor
}

func NewStarknet(dependency libpak.BuildpackDependency, cache libpak.DependencyCache, configResolver libpak.ConfigurationResolver) Starknet {
	contributor := libpak.NewDependencyLayerContributor(dependency, cache, libcnb.LayerTypes{
		Cache:  true,
		Launch: true,
	})
	return Starknet{
		LayerContributor: contributor,
		configResolver:   configResolver,
		Executor:         effect.NewExecutor(),
	}
}

func (r Starknet) Contribute(layer libcnb.Layer) (libcnb.Layer, error) {
	r.LayerContributor.Logger = r.Logger
	return r.LayerContributor.Contribute(layer, func(artifact *os.File) (libcnb.Layer, error) {
		bin := filepath.Join(layer.Path, "bin")

		r.Logger.Bodyf("Expanding %s to %s", artifact.Name(), bin)
		if err := crush.Extract(artifact, bin, 0); err != nil {
			return libcnb.Layer{}, fmt.Errorf("unable to expand %s\n%w", artifact.Name(), err)
		}

		// Must be set to executable
		file := filepath.Join(bin, PlanEntryStarkli)
		r.Logger.Bodyf("Setting %s as executable", file)
		if err := os.Chmod(file, 0755); err != nil {
			return libcnb.Layer{}, fmt.Errorf("unable to chmod %s\n%w", file, err)
		}

		// Must be set to PATH
		r.Logger.Bodyf("Setting %s in PATH", bin)
		if err := os.Setenv("PATH", sherpa.AppendToEnvVar("PATH", ":", bin)); err != nil {
			return libcnb.Layer{}, fmt.Errorf("unable to set $PATH\n%w", err)
		}

		// get starkli version
		buf, err := r.Execute(PlanEntryStarkli, []string{"--version"})
		if err != nil {
			return libcnb.Layer{}, fmt.Errorf("unable to get %s version\n%w", PlanEntryStarkli, err)
		}
		version := strings.TrimSpace(buf.String())
		r.Logger.Bodyf("Checking %s version: %s", PlanEntryStarkli, version)

		// initialize wallet for deploy
		if ok, err := r.InitializeDeployWallet(); !ok {
			return libcnb.Layer{}, err
		}

		deployPrivateKey, _ := r.configResolver.Resolve("BP_STARKNET_DEPLOY_PRIVATE_KEY")
		deployAccount, _ := r.configResolver.Resolve("BP_STARKNET_DEPLOY_ACCOUNT")
		deployRpc, _ := r.configResolver.Resolve("BP_STARKNET_DEPLOY_RPC")

		layer.LaunchEnvironment.Append("PATH", ":", bin)
		layer.LaunchEnvironment.Default("STARKNET_PRIVATE_KEY", deployPrivateKey)
		layer.LaunchEnvironment.Default("STARKNET_ACCOUNT", deployAccount)
		layer.LaunchEnvironment.Default("STARKNET_RPC", deployRpc)
		return layer, nil
	})
}

func (r Starknet) Execute(command string, args []string) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	if err := r.Executor.Execute(effect.Execution{
		Command: command,
		Args:    args,
		Stdout:  buf,
		Stderr:  buf,
	}); err != nil {
		return buf, fmt.Errorf("%s: %w", buf.String(), err)
	}
	return buf, nil
}

func (r Starknet) BuildProcessTypes(cr libpak.ConfigurationResolver, app libcnb.Application) ([]libcnb.Process, error) {
	processes := []libcnb.Process{}

	enableDeploy := cr.ResolveBool("BP_ENABLE_STARKNET_DEPLOY")
	if enableDeploy {
		deployPrivateKey, _ := r.configResolver.Resolve("BP_STARKNET_DEPLOY_PRIVATE_KEY")
		if len(deployPrivateKey) == 0 {
			return processes, fmt.Errorf("BP_STARKNET_DEPLOY_PRIVATE_KEY must be specified")
		}

		if classHash, err := r.DeclareContract(); err != nil {
			deployWalletAddress, _ := r.configResolver.Resolve("BP_STARKNET_DEPLOY_WALLET_ADDRESS")
			processes = append(processes, libcnb.Process{
				Type:      PlanEntryStarkli,
				Command:   PlanEntryStarkli,
				Arguments: []string{"deploy", classHash, deployWalletAddress},
				Default:   true,
			})
		}
	}
	return processes, nil
}

func (r Starknet) Name() string {
	return r.LayerContributor.LayerName()
}

func (r Starknet) InitializeDeployWallet() (bool, error) {
	enableDeploy := r.configResolver.ResolveBool("BP_ENABLE_STARKNET_DEPLOY")
	if enableDeploy {
		return r.InitializeWallet()
	}
	return true, nil
}

/**
 * starkli account fetch <SMART_WALLET_ADDRESS>
 *	--output ~/.starkli-wallets/deployer/my_account_1.json
 *	--rpc https://starknet-sepolia.public.blastapi.io/rpc/v0_7
 */
func (r Starknet) InitializeWallet() (bool, error) {
	deployWalletAddress, _ := r.configResolver.Resolve("BP_STARKNET_DEPLOY_WALLET_ADDRESS")
	deployAccount, _ := r.configResolver.Resolve("BP_STARKNET_DEPLOY_ACCOUNT")
	deployRpc, _ := r.configResolver.Resolve("BP_STARKNET_DEPLOY_RPC")

	accountDir := filepath.Dir(deployAccount)
	r.Logger.Bodyf("Initializing deploy wallet and save to dir:", accountDir)
	os.MkdirAll(accountDir, os.ModePerm)

	args := []string{
		"account",
		"fetch",
		deployWalletAddress,
		"--output",
		deployAccount,
		"--rpc",
		deployRpc,
	}

	if _, err := r.Execute(PlanEntryStarkli, args); err != nil {
		return false, fmt.Errorf("unable to initialize deploy wallet\n%w", err)
	}
	return true, nil
}

func (r Starknet) DeclareContract() (string, error) {
	r.Logger.Bodyf("Declaring contract")
	args := []string{
		"declare",
		"target/dev/*.contract_class.json",
	}

	buf, err := r.Execute(PlanEntryStarkli, args)
	if err != nil {
		return "", fmt.Errorf("unable to declaring contract\n%w", err)
	}

	classHash := strings.TrimSpace(buf.String())
	return classHash, nil
}
