This folder is a [kustomize application](https://kubectl.docs.kubernetes.io/references/kustomize/glossary/#application) that stands up a running instance of go-httpbin in a Kubernetes cluster.

You may wish to utilise this as a [remote application](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/resource/), e.g.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
commonLabels:
  app.kubernetes.io/name: httpbin
resources:
  - github.com/mccutchen/go-httpbin/kustomize
images:
  - name: mccutchen/go-httpbin
```

To expose your instance to the internet, you could add an `Ingress` in an overlay:
```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: httpbin
spec:
  ingressClassName: myingressname
  rules:
    - host: my-go-httpbin.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: httpbin
                port:
                  name: http
  tls:
   - hosts:
       - my-go-httpbin.com
     secretName: go-httpbin-tls
```
