# Cedar access controls for Kubernetes Demo

- [Cedar access controls for Kubernetes Demo](#cedar-access-controls-for-kubernetes-demo)
  - [Motivation](#motivation)
  - [Setup](#setup)
  - [Authorization](#authorization)
    - [Basic access](#basic-access)
    - [Group access](#group-access)
    - [Attribute-based access control](#attribute-based-access-control)
    - [Impersonation](#impersonation)
  - [Admission](#admission)
    - [Name selection](#name-selection)
    - [Key/Value maps](#keyvalue-maps)

## Motivation

Administrators who want to secure their Kubernetes clusters today have to learn and use multiple different policy languages, and ensure those policies are individually applied on all their clusters.
For example, if an administrator wants to allow users to create deployments, but prevent them from creating pods that don't have a required label pair, they must write two policies in separate languages: one authorization policy permitting pod creation, and another validation policy preventing pods with offending label.

An example authorization RBAC policy might look like:
```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: deployment-manager
rules:
- apiGroups:
  - "apps"
  resources:
  - deployments
  verbs:
  - "*"
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: deployment-manager
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: deployment-manager
subjects:
- apiGroup: rbac.authorization.k8s.io
  kind: Group
  name: developers
```

With an Open Policy Agent/Gatekeeper policy written in Rego like so:
```rego
package k8srequiredlabels

get_message(parameters, _default) := _default {
    not parameters.message
}

get_message(parameters, _) := parameters.message

violation[{"msg": msg, "details": {"missing_labels": missing}}] {
    provided := {label | input.review.object.metadata.labels[label]}
    required := {label | label := input.parameters.labels[_].key}
    missing := required - provided
    count(missing) > 0
    def_msg := sprintf("you must provide labels: %v", [missing])
    msg := get_message(input.parameters, def_msg)
}

violation[{"msg": msg}] {
    value := input.review.object.metadata.labels[key]
    expected := input.parameters.labels[_]
    expected.key == key
    # do not match if allowedRegex is not defined, or is an empty string
    expected.allowedRegex != ""
    not regex.match(expected.allowedRegex, value)
    def_msg := sprintf("Label <%v: %v> does not satisfy allowed regex: %v", [key, value, expected.allowedRegex])
    msg := get_message(input.parameters, def_msg)
}
```
(borrowed from the [OPA Gatekeeper library][gk-lib])

[gk-lib]: https://open-policy-agent.github.io/gatekeeper-library/website/validation/requiredlabels

Defining permit actions in one file and restrictions in separate policy files, languages, and frameworks introduces high cognitive overhead to administrators tasked with defending their clusters.
The risk of an unintended effect increases when writing and reviewing code changes to existing policies, as a reviewer might not be aware of all permissions or restrictions if only one is being modified.

Cedar access control for Kubernetes helps solve these problems.
By using the same language for both authorization and admission policies, administrators can quickly reason about what permissions are granted and what restrictions are applied in the same policy file.
Additionally, policies can be specified outside of a cluster and apply to whole fleets of clusters.
This gives administrators powerful and unmatched new tools to secure their clusters.

## Setup

_Note: This demo assumes you've already installed both the authorizer and admission webhook as described in `docs/Setup.md`, and have the test-user kubeconfig created._

You can validate that the test-user can access the cluster by running a whoami
```bash
$ KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl auth whoami
ATTRIBUTE   VALUE
Username    test-user
Groups      [viewers test-group system:authenticated]
```

## Authorization

### Basic access

Lets write two policies for our test-user:
```cedar
@description("test-user can get/list/watch pods")
permit (
    principal,
    action in [k8s::Action::"get", k8s::Action::"list", k8s::Action::"watch"],
    resource is k8s::Resource
) when {
    principal.name == "test-user" &&
    resource.apiGroup == "" &&
    resource.resource == "pods"
};

@description("forbid test-user to get/list/watch nodes"
forbid (
    principal,
    action in [k8s::Action::"get", k8s::Action::"list", k8s::Action::"watch"],
    resource is k8s::Resource
) when {
    principal.name == "test-user" &&
    resource.apiGroup == "" &&
    resource.resource == "nodes"
};
```

```bash
# Try getting resources
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl get pods -A
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl get nodes
```

While we could equivalently express the first policy in RBAC, the second rule is not possible, as RBAC does not support denials.
```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: basic-rule
rules:
- apiGroups:
  - ""
  resources:
  - pods
  verbs:
  - get
  - list
  - watch
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: basic-rule
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: basic-rule
subjects:
- apiGroup: rbac.authorization.k8s.io
  kind: User
  name: test-user
```

### Group access

Given that the test-user is in the group `viewers`, lets leverage that group by writing a policy for it:

```cedar
// viewer group members can get/list/watch any Resource other than secrets in the default namespace
permit (
    principal in k8s::Group::"viewers",
    action in [ k8s::Action::"get", k8s::Action::"list", k8s::Action::"watch"],
    resource is k8s::Resource
) unless {
    resource.resource == "secrets" &&
    resource.namespace == "default" &&
    resource.apiGroup == ""
};
```

```bash
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl get serviceaccounts
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl get secrets
```

### Attribute-based access control

Kubernetes 1.31 added alpha support for evaluating labels and field selectors in authorization.
One feature of Cedar is that conditions can not only reference the resource or principal of a policy, they can reference both on opposite sides of an operator.
Lets try it out!

```cedar
// We allow users to list and watch secrets when the request
// includes the label selector owner=={request.user.name}
permit (
    principal is k8s::User,
    action in [k8s::Action::"list", k8s::Action::"watch"],
    resource is k8s::Resource
) when {
    resource.resource == "secrets" &&
    resource.apiGroup == "" &&
    resource has labelSelector &&
    resource.labelSelector.containsAny([
        {"key": "owner","operator": "=", "values": [principal.name]},
        {"key": "owner","operator": "==", "values": [principal.name]},
        {"key": "owner","operator": "in", "values": [principal.name]}])
};
```

```bash
# as Admin we can see all secrets and display their labels
kubectl get secrets --show-labels

# Try to list secrets as the test-user
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl get secrets
# Try to list secrets as the test user using a label selector
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl get secrets -l owner=test-user --show-labels
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl get secrets -l owner==test-user --show-labels
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl get secrets -l 'owner in (test-user)' --show-labels
```

### Impersonation

Kubernetes supports [impersonation][k8s-impersonation], where you can act as a different user or group using a special header by specifying the `--as` or `--as-group` flag in `kubectl`. Lets see a Cedar policy for this:

[k8s-impersonation]: https://kubernetes.io/docs/reference/access-authn-authz/authentication/#user-impersonation

```cedar
// test-user can impersonate the service-manager service account in the default namespace
permit (
    principal is k8s::User,
    action in [k8s::Action::"impersonate"],
    resource is k8s::ServiceAccount
) when {
    principal.name == "test-user" &&
    resource.namespace == "default" &&
    resource.name == "service-manager"
};

// SA named 'service-manager' can act on services in its own namespace
permit (
    principal is k8s::ServiceAccount,
    action,
    resource is k8s::Resource
) when {
    principal.name == "service-manager" && // no principal.namespace restriction
    resource.resource == "services" &&
    resource.namespace == principal.namespace
};
```

```bash
# check if you can impersonate the service account service-manager
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl auth can-i impersonate serviceaccount/service-manager
# check if you can create a service
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl auth can-i create service

# Impersonate a request as the service-manager
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl get configmap --as system:serviceaccount:default:service-manager

# Check if you can create a service as the service-manager
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl auth can-i create service --as system:serviceaccount:default:service-manager

# Create a service
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl --as system:serviceaccount:default:service-manager create service clusterip my-cool-service --tcp 80:80
```

## Admission

Run the following to apply the demonstrated policies
```bash
kubectl apply -f demo/admission-policy.yaml
```

Note: Cedar's default decision is to deny requests if there are no matching `permit` or `forbid` policies, and that explicit `forbid`s have precedence over any matching `permit`.
This works well for authorization, but less so for admission.
Because requests have already been authorized, we want to allow them by default without the user having to say so.
The admission webhook automatically applies a policy that allows all admission requests, so users only need to write `forbid` policies.
```cedar
@description("default allow-all admission rule")
permit (
  principal,
  action in [
    k8s::admission::Action::"create",
    k8s::admission::Action::"update",
    k8s::admission::Action::"delete",
    k8s::admission::Action::"connect",
  ],
  resource
);
```

### Name selection

Because all policies are written in Cedar, we can write both authentication and authorization policies in the same language and even same file.

```cedar
// Authorization policy
// test-user can do Action::"*" on configmaps in the default namespace
permit (
    principal is k8s::User,
    action,
    resource is k8s::Resource
) when {
    principal.name == "test-user" &&
    resource.namespace == "default" &&
    resource.apiGroup == "" &&
    resource.resource == "configmaps"
};

// Admission policy preventing test-user from creating/updating configmaps with name starting with "prod"
forbid (
    principal is k8s::User,
    action in [k8s::admission::Action::"create", k8s::admission::Action::"update"],
    resource is core::v1::ConfigMap
) when {
    principal.name == "test-user" &&
    resource.metadata.name like "prod*"
};
```

Lets try this out:
```bash
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl get configmap
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl create configmap test-config --from-literal=stage=test

# Try creating with a name starting with "prod"
KUBECONFIG=./mount/test-user-kubeconfig.yaml kubectl create configmap prod-config --from-literal=stage=prod
```

These types of admission rules are already possible today with general purpose tools like Open Policy Agent (OPA) or Kyverno, but each requires learning and writing separate domain languages than what authorization policies are defined in (RBAC).

### Key/Value maps

For now, the webhook modifies labels and annotations into a list of structure with the fields "key" and "value", so policy can be written against it.
Once cedar-go [adds support for entity tags][cedar-go-entity-tags], we'll refactor to use that structure and syntax.
That will be a breaking change to any policy using the current key/value structure.

[cedar-go-entity-tags]: https://github.com/cedar-policy/cedar-go/issues/47

```cedar
// authorization policy allowing sample-user to make actions on configmaps in default namespace
// sample-user is in the `requires-labels` group
permit (
    principal is k8s::User,
    action in [
        k8s::Action::"create",
        k8s::Action::"list",
        k8s::Action::"watch",
        k8s::Action::"update",
        k8s::Action::"patch",
        k8s::Action::"delete"],
    resource is k8s::Resource
) when {
    principal.name == "sample-user" &&
    resource has namespace &&
    resource.namespace == "default" &&
    resource.apiGroup == "" &&
    resource.resource == "configmaps"
};

// authz policy forbiding users in group "requires-labels" from making list/watches
// without label selector owner={principal.name}
forbid (
    principal is k8s::User,
    action in [k8s::Action::"list", k8s::Action::"watch"],
    resource is k8s::Resource
) when {
    principal in k8s::Group::"requires-labels"
} unless {
    resource has labelSelector &&
    resource.labelSelector.contains(
        {"key": "owner","operator": "=", "values": [principal.name]})
};

// admission policy to forbid resource creation without an owner key
forbid (
    principal is k8s::User,
    action in [k8s::admission::Action::"create", k8s::admission::Action::"update"],
    resource
) when {
    principal in k8s::Group::"requires-labels"
} unless {
    resource has metadata &&
    resource.metadata has labels &&
    resource.metadata.labels.contains({"key": "owner", "value": principal.name})
};

// admission policy forbidding users in "requires-labels" group from updating or deleting
// resources that they don't already have the owner={principal.name} label
forbid (
    principal is k8s::User,
    action in [k8s::admission::Action::"delete", k8s::admission::Action::"update"],
    resource
) when {
    principal in k8s::Group::"requires-labels"
} unless {
    context has oldObject &&
    context.oldObject has metadata &&
    context.oldObject.metadata has labels &&
    context.oldObject.metadata.labels.contains({"key": "owner", "value": principal.name})
};
```

Lets try this out:
```bash
# as admin
kubectl create configmap other-config --from-literal=foo=bar
kubectl label cm/other-config owner=some-user
kubectl get cm --show-labels

# Try to modify the configmap as sample-user
KUBECONFIG=./mount/sample-user-kubeconfig.yaml kubectl auth whoami
KUBECONFIG=./mount/sample-user-kubeconfig.yaml kubectl label cm/other-config stage=test
KUBECONFIG=./mount/sample-user-kubeconfig.yaml kubectl get cm

KUBECONFIG=./mount/sample-user-kubeconfig.yaml kubectl create cm sample-config --from-literal=k1=v1

KUBECONFIG=./mount/sample-user-kubeconfig.yaml kubectl get cm -l owner=sample-user --show-labels
cat << EOF > sample-config.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: sample-config
  labels:
    owner: sample-user
data:
  stage: test
EOF
KUBECONFIG=./mount/sample-user-kubeconfig.yaml kubectl create -f ./sample-config.yaml
KUBECONFIG=./mount/sample-user-kubeconfig.yaml kubectl get cm -l owner=sample-user --show-labels
# try to change the owner
KUBECONFIG=./mount/sample-user-kubeconfig.yaml kubectl patch configmap/sample-config -p '{"metadata":{"labels":{"owner":"other-user"}}}'
```
