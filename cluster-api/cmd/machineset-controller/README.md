# MachineSet Controller
is an initial implementation of `MachineSet` controller. It watches for `MachineSet` resources
and makes sure that the current state is equal to the desired one. At the moment the current state of the cluster
is calculated based on the number of machines that are owned by the given machineSet resource.
This essentially means that newly created resources have their `OwnerReferences`  field set to the given `MachineSet` resource.

# Quickstart

## Creating CRD types
to create required `CustomResourceDefinitions` inside your cluster please run the following command.
```bash
./machines-crd-installer -kubeconfig=/path/to/config -alsologtostderr
```

## Creating a machineset resource
`kubectl apply -f examples/manifests/machinesets.yaml`

# Development

## Building
```bash
make
```

## Running
```bash
./machineset-controller -kubeconfig=/path/to/config -alsologtostderr -v=2
```
