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

// Controller is a implementation of a machine controller which creates machine resources
type Controller struct {
	// machineClient a client that knows how to consume Machine resources
	machineClient machineclientset.Interface

	// machineSetsInformer holds a shared informer for MachineSets
	machineSetsInformer cache.SharedIndexInformer

	// machineSetsLister holds a lister that knows how to list MachineSets from a cache
	machineSetsLister machinelistersv1alpha1.MachineSetLister

	// machineLister holds a lister that knows how to list Machines from a cache
	machineLister machinelistersv1alpha1.MachineLister

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.RateLimitingInterface
}

// New returns an instance of the MachineSet controller
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

// Run starts the control loop. This method is blocking.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()
	defer c.queue.ShutDown()

	glog.Info("starting machineset controller")
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	glog.Info("shutting down machineset controller")
}

func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem deals with one key off the queue.  It returns false
// when it's time to quit.
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

// syncHandler holds the controller's business logic.
// it makes sure that the current state is equal to the desired state.
// note that the current state of the cluster is calculated based on the number of machines
// that are owned by the given machineSet (key).
func (c *Controller) syncHandler(key string) error {
	machineSet, err := c.machineSetsLister.Get(key)
	if err != nil {
		return err
	}

	filteredMachines, err := c.getMachines(machineSet)
	if err != nil {
		return err
	}

	return c.syncReplicas(machineSet, filteredMachines)
}

// syncReplicas essentially scales machine resources up and down.
func (c *Controller) syncReplicas(machineSet *v1alpha1.MachineSet, machines []*v1alpha1.Machine) error {
	currentMachineCount := int32(len(machines))
	desiredReplicas := int32(0)
	if machineSet.Spec.Replicas != nil {
	 desiredReplicas = *machineSet.Spec.Replicas
	}

	if desiredReplicas > currentMachineCount {
		glog.V(2).Infof("creating a machine ( spec.replicas(%d) > currentMachineCount(%d) )", desiredReplicas, currentMachineCount)
		machine, err := c.createMachine(machineSet)
		if err != nil {
			return err
		}
		_, err = c.machineClient.ClusterV1alpha1().Machines().Create(machine)
		if err != nil {
			return err
		}
		if err = waitForMachineInCache(machine.Name, c.machineLister); err != nil {
			return err
		}
		return nil
	}

	if desiredReplicas < currentMachineCount {
		glog.V(2).Infof("deleting a machine ( spec.replicas(%d) < currentMachineCount(%d) )", desiredReplicas, currentMachineCount)
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

// createMachine creates a machine resource.
// the name of the newly created resource is created as machineSet.Name + some random characters.
// in addition, the newly created resource is owned by the given machineSet (we set the OwnerReferences field)
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

// getMachines returns a list of machines that match on machineSet.UID
func (c *Controller) getMachines(machineSet *clusterv1alpha1.MachineSet) ([]*v1alpha1.Machine, error) {
	filteredMachines := []*v1alpha1.Machine{}
	allMachines, err := c.machineLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	for _, machine := range allMachines {
		oRef := metav1.GetControllerOf(machine)
		if oRef == nil {
			continue
		}
		if oRef.UID == machineSet.UID {
			filteredMachines = append(filteredMachines, machine)
		}
	}
	return filteredMachines, nil
}
