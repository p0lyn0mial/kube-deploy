# MachineSet Controller

# Quickstart

## Creating CRD types
```bash
./machines-crd-installer -kubeconfig=/path/to/config -alsologtostderr
```
`kubectl apply -f examples/manifests/machinesets.yaml`

## Creating a machineset resource
`kubectl apply -f examples/manifests/machinesets.yaml`

# Development

## Building
```bash
make
```

## Running
```bash
./machineset-controller -kubeconfig=/path/to/config -alsologtostderr
```
