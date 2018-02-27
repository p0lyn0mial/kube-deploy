/*
Copyright 2018 The Kubernetes Authors.

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

package machineset

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/wait"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kube-deploy/cluster-api/api/cluster/v1alpha1"
	"k8s.io/kube-deploy/cluster-api/client/clientset/versioned/fake"
	machineinformers "k8s.io/kube-deploy/cluster-api/client/informers/externalversions"
	v1alpha1listers "k8s.io/kube-deploy/cluster-api/client/listers/cluster/v1alpha1"
)

func TestMachineSetControllerSyncHanlder(t *testing.T) {
	tests := []struct {
		name                string
		startingMachineSets []*v1alpha1.MachineSet
		startingMachines    []*v1alpha1.Machine
		machineSetToSync    string
		expectedMachine     *v1alpha1.Machine
		expectedActions     []string
	}{
		{
			name:                "scenario 1: the current state of the cluster is empty, thus a machine is created.",
			startingMachineSets: []*v1alpha1.MachineSet{createMachineSet(3, "foo", "bar1")},
			startingMachines:    nil,
			machineSetToSync:    "foo",
			expectedActions:     []string{"list", "watch", "create"},
			expectedMachine:     machineFromMachineSet(createMachineSet(3, "foo", "bar1"), "bar1"),
		},
		{
			name:                "scenario 2: the current state of the cluster is too small, thus a machine is created.",
			startingMachineSets: []*v1alpha1.MachineSet{createMachineSet(3, "foo", "bar3")},
			startingMachines:    []*v1alpha1.Machine{machineFromMachineSet(createMachineSet(3, "foo", "bar1"), "bar1"), machineFromMachineSet(createMachineSet(3, "foo", "bar2"), "bar2")},
			machineSetToSync:    "foo",
			expectedActions:     []string{"list", "watch", "create"},
			expectedMachine:     machineFromMachineSet(createMachineSet(3, "foo", "bar3"), "bar3"),
		},
		{
			name:                "scenario 3: the current state of the cluster is equal to the desired one, no machine resource is created.",
			startingMachineSets: []*v1alpha1.MachineSet{createMachineSet(2, "foo", "bar3")},
			startingMachines:    []*v1alpha1.Machine{machineFromMachineSet(createMachineSet(2, "foo", "bar1"), "bar1"), machineFromMachineSet(createMachineSet(2, "foo", "bar2"), "bar2")},
			machineSetToSync:    "foo",
			expectedActions:     []string{"list", "watch"},
		},
		{
			name:                "scenario 4: the current state of the cluster is bigger than the desired one, thus a machine is deleted.",
			startingMachineSets: []*v1alpha1.MachineSet{createMachineSet(0, "foo", "bar2")},
			startingMachines:    []*v1alpha1.Machine{machineFromMachineSet(createMachineSet(1, "foo", "bar1"), "bar1")},
			machineSetToSync:    "foo",
			expectedActions:     []string{"list", "watch", "delete"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// setup the test scenario
			rObjects := []runtime.Object{}
			machinesIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			for _, amachine := range test.startingMachines {
				err := machinesIndexer.Add(amachine)
				if err != nil {
					t.Fatal(err)
				}
				rObjects = append(rObjects, amachine)
			}
			machineSetIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			for _, amachineset := range test.startingMachineSets {
				err := machineSetIndexer.Add(amachineset)
				if err != nil {
					t.Fatal(err)
				}
			}

			fakeClient := fake.NewSimpleClientset(rObjects...)
			machineInformerFactory := machineinformers.NewSharedInformerFactory(fakeClient, 0)
			machineLister := v1alpha1listers.NewMachineLister(machinesIndexer)
			machineSetLister := v1alpha1listers.NewMachineSetLister(machineSetIndexer)
			machineSetsInformer := machineInformerFactory.Cluster().V1alpha1().MachineSets().Informer()
			machineInformerFactory.Start(wait.NeverStop)
			cache.WaitForCacheSync(wait.NeverStop, machineSetsInformer.HasSynced)
			target := New(fakeClient, machineSetsInformer, machineSetLister, machineLister)

			// act
			err := target.syncHandler(test.machineSetToSync)
			if err != nil {
				t.Fatal(err)
			}

			// validate
			actions := fakeClient.Actions()
			if len(actions) != len(test.expectedActions) {
				t.Fatalf("unexpected actions: %v, expected %d actions got %d", actions, len(test.expectedActions), len(actions))
			}
			for i, verb := range test.expectedActions {
				if actions[i].GetVerb() != verb {
					t.Fatalf("unexpected action: %v, expected %s", actions[i], verb)
				}
			}

			if test.expectedMachine != nil {
				// we take only the first item in line
				var actualMachine *v1alpha1.Machine
				for _, action := range actions {
					if action.GetVerb() == "create" {
						createAction, ok := action.(clienttesting.CreateAction)
						if !ok {
							t.Fatalf("unexpected action %#v", action)
						}
						actualMachine = createAction.GetObject().(*v1alpha1.Machine)
						break
					}
				}

				if !equality.Semantic.DeepEqual(actualMachine, test.expectedMachine) {
					t.Fatalf("acutal machine is different from the expected one: %v", diff.ObjectDiff(test.expectedMachine, actualMachine))
				}
			}
		})
	}
}

func createMachineSet(replicas int, machineSetName string, machineName string) *v1alpha1.MachineSet {
	replicasInt32 := int32(replicas)
	return &v1alpha1.MachineSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "MachineSet",
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: machineSetName,
		},
		Spec: v1alpha1.MachineSetSpec{
			Replicas: &replicasInt32,
			Template: v1alpha1.MachineTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
				  Name: machineName,
				},
				Spec: v1alpha1.MachineSpec{
					ProviderConfig: "some provider specific configuration data",
				},
			},
		},
	}
}

func machineFromMachineSet(machineSet *v1alpha1.MachineSet, name string) *v1alpha1.Machine {
	amachine := &v1alpha1.Machine{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Machine",
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: machineSet.Spec.Template.ObjectMeta,
		Spec: machineSet.Spec.Template.Spec,
	}

	amachine.Name = name
	amachine.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(machineSet, v1alpha1.SchemeGroupVersion.WithKind("MachineSet"))}

	return amachine
}
