kubectl logs -f $(kubectl get pods -o name | grep undermoon-operator | grep -v Terminating) -c manager
