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
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	clusterv1alpha1 "k8s.io/kube-deploy/cluster-api/api/cluster/v1alpha1"
	machineclientset "k8s.io/kube-deploy/cluster-api/client/clientset/versioned"
	machinelistersv1alpha1 "k8s.io/kube-deploy/cluster-api/client/listers/cluster/v1alpha1"

	"github.com/golang/glog"
	"k8s.io/kube-deploy/cluster-api/api/cluster/v1alpha1"
)

// TODO: desc
type Controller struct {
	// TODO: change it to MachineInterface
	machineClient machineclientset.Interface

	// TODO: desc
	machineSetsInformer cache.SharedIndexInformer

	// TODO: desc
	machineSetsLister machinelistersv1alpha1.MachineSetLister

	// TODO: desc
	machineLister machinelistersv1alpha1.MachineLister

	// TODO: desc
	queue workqueue.RateLimitingInterface
}

// TODO: desc
func New(machineClient machineclientset.Interface, machineSetsInformer cache.SharedIndexInformer, machineSetsLister machinelistersv1alpha1.MachineSetLister, machineLister machinelistersv1alpha1.MachineLister) *Controller {
	controller := &Controller{
		queue:               workqueue.NewNamedRateLimitingQueue(workqueue.NewItemFastSlowRateLimiter(2*time.Second, 10*time.Second, 5), "MachineSets"),
		machineClient:       machineClient,
		machineSetsInformer: machineSetsInformer,
		machineSetsLister:   machineSetsLister,
		machineLister:       machineLister,
	}

	controller.machineSetsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				controller.queue.AddRateLimited(key)
			}
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				controller.queue.AddRateLimited(key)
			}
		},
	})

	return controller
}

// Run begins watching and syncing.
// TODO: desc
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()
	defer c.queue.ShutDown()

	glog.Info("starting machine controller")
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	glog.Info("shutting down machine controller")
}

func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *Controller) processNextWorkItem() bool {
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(key)

	err := c.syncHandler(key.(string))
	if err == nil {
		c.queue.Forget(key)
		return true
	}

	runtime.HandleError(fmt.Errorf("%v failed with: %v", key, err))
	c.queue.AddRateLimited(key)

	return true
}

// TODO: desc
func (c *Controller) syncHandler(key string) error {
	machineSet, err := c.machineSetsLister.Get(key)
	if err != nil {
		return err
	}

	var currentMachines []*v1alpha1.Machine
	{
		allMachines, err := c.machineLister.List(labels.Everything())
		if err != nil {
			return err
		}
		for _, machine := range allMachines {
			oRef := metav1.GetControllerOf(machine)
			if oRef == nil {
				continue
			}
			if oRef.UID == machineSet.UID {
				currentMachines = append(currentMachines, machine)
			}
		}
	}

	return c.syncReplicas(machineSet, currentMachines)
}

// TODO: create only one item at a time
func (c *Controller) syncReplicas(machineSet *v1alpha1.MachineSet, machines []*v1alpha1.Machine) error {
	currentMachineCount := int32(len(machines))
	desiredReplicas := int32(0)
	if machineSet.Spec.Replicas != nil {
	 desiredReplicas = *machineSet.Spec.Replicas
	}

	if desiredReplicas > currentMachineCount {
		glog.Infof("creating a machine ( spec.replicas(%d) > currentMachineCount(%d) )", desiredReplicas, currentMachineCount)
		machine, err := c.createMachine(machineSet)
		if err != nil {
			return err
		}
		_, err = c.machineClient.ClusterV1alpha1().Machines().Create(machine)
		if err != nil {
			return err
		}
		if err = waitForNodeInCache(machine.Name, c.machineLister); err != nil {
			return err
		}
		return nil
	}

	if desiredReplicas < currentMachineCount {
		glog.Infof("deleting a machine ( spec.replicas(%d) < currentMachineCount(%d) )", desiredReplicas, currentMachineCount)
		machineToDelete := machines[0].Name
		err := c.machineClient.ClusterV1alpha1().Machines().Delete(machineToDelete, &metav1.DeleteOptions{})
		if err != nil {
			return err
		}
		if err = waitForMachineDeletedInCache(machineToDelete, c.machineLister); err != nil {
			return err
		}
		return nil
	}

	return nil
}

func (c *Controller) createMachine(machineSet *clusterv1alpha1.MachineSet) (*clusterv1alpha1.Machine, error) {
	machineName := machineSet.Name + "-" + string(uuid.NewUUID())[:6]

	gv := clusterv1alpha1.SchemeGroupVersion
	machine := &clusterv1alpha1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:            machineName,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(machineSet, gv.WithKind("MachineSet"))},
		},
		Spec: machineSet.Spec.Template.Spec,
	}

	return machine, nil
}
