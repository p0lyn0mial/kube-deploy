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

package main

import (
	"flag"
	"reflect"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	machineclientset "k8s.io/kube-deploy/cluster-api/client/clientset/versioned"
	machineinformers "k8s.io/kube-deploy/cluster-api/client/informers/externalversions"
	"k8s.io/kube-deploy/cluster-api/pkg/controller/machineset"

	"github.com/golang/glog"
	"k8s.io/kube-deploy/cluster-api/pkg/signals"
)


func main() {
	var kubeconfig string
	var workerCount   int

	flag.StringVar(&kubeconfig, "kubeconfig", "", "a path to a kubeconfig. Only required if out-of-cluster.")
	flag.IntVar(&workerCount, "worker-count", 4, "the number of workers that are allowed to work concurrently. Larger number = more responsive the controller will be, but it will use more CPU and network.")
	flag.Parse()

	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		glog.Fatalf("error building kubeconfig: %v", err)
	}

	machineClient, err := machineclientset.NewForConfig(cfg)
	if err != nil {
		glog.Fatalf("error building clientset for machineClient: %v", err)
	}

	machineInformerFactory := machineinformers.NewSharedInformerFactory(machineClient, time.Second*30)
	machineSetsInformer := machineInformerFactory.Cluster().V1alpha1().MachineSets().Informer()
	machineSetsLister := machineInformerFactory.Cluster().V1alpha1().MachineSets().Lister()
	machineLister := machineInformerFactory.Cluster().V1alpha1().Machines().Lister()

	go machineInformerFactory.Start(stopCh)
	for _, syncsMap := range []map[reflect.Type]bool{machineInformerFactory.WaitForCacheSync(stopCh)} {
		for key, synced := range syncsMap {
			if !synced {
				glog.Fatalf("unable to sync %s", key)
			}
		}
	}

	controller := machineset.New(machineClient, machineSetsInformer, machineSetsLister, machineLister)
	controller.Run(workerCount, stopCh)
}
