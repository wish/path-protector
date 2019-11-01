# path-protector [![GoDoc](https://godoc.org/github.com/wish/path-protector?status.svg)](https://godoc.org/github.com/wish/path-protector) [![Build Status](https://travis-ci.org/wish/path-protector.svg?branch=master)](https://travis-ci.org/wish/path-protector)  [![Go Report Card](https://goreportcard.com/badge/github.com/wish/path-protector)](https://goreportcard.com/report/github.com/wish/path-protector)  [![Docker Repository on Quay](https://quay.io/repository/wish/path-protector/status "Docker Repository on Quay")](https://quay.io/repository/wish/path-protector)

path-protector is a [MutatingAdmissionWebhook](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#mutatingadmissionwebhook) service that will "protect" the current values of objects from being overwriten as configured in annotations.


Our initial driving use-case was supporting `kubectl apply` with deployments that use HPA. Although there was lots of [discussion upstream](https://github.com/kubernetes/kubernetes/issues/25238) there weren't great solutions to the issue. As we dug into it the issue we found boils down to 2 fields in the manifests being overwritten -- when in reality we want them to remain the same. So we decided to create this simple webhook to protect defined paths from being overwritten in an `apply`. This way we can simply protect `/metadata/annotations/autoscaling.alpha.kubernetes.io/current-metrics` on the HPA and `/spec/replicas` on the deployment and you can now do `kubectl apply` on an HPA deployment without causing issues.


## How to use it

``` yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hw
  namespace: hw
  labels:
    app: hw
    # this label enables the webhook to run on this object
    path-protector.wish.com/enabled: 'true'
  annotations:
    # this annotation defines a comma-seperated list of paths to protect
    path-protector.wish.com/paths: '/spec/replicas'
spec:
  replicas: 105
  selector:
    matchLabels:
      app: hw
  template:
    metadata:
      labels:
        app: hw
    spec:
      containers:
      - name: hw
        image: smcquay/hw:v0.1.2
```
