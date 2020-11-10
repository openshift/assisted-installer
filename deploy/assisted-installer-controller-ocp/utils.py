import logging
import os
import subprocess

LOCAL_TARGET = 'minikube'
OCP_TARGET = 'ocp'

KUBECTL_CMD = 'kubectl'


def verify_build_directory(namespace):
    dirname = os.path.join(os.getcwd(), 'build', namespace)
    if os.path.isdir(dirname):
        return
    os.makedirs(dirname)
    logging.info('Created build directory: %s', dirname)


def get_logger(name, level=logging.INFO):
    fmt = '[%(levelname)s] %(asctime)s - %(name)s - %(message)s'
    formatter = logging.Formatter(fmt)
    sh = logging.StreamHandler()
    sh.setFormatter(formatter)
    log = logging.getLogger(name)
    log.setLevel(level)
    log.addHandler(sh)
    return log


def check_output(cmd):
    return subprocess.check_output(cmd, shell=True).decode("utf-8")


def apply(target, namespace, profile, file):
    kubectl_cmd = get_kubectl_command(target, namespace, profile)
    print(check_output(f'{kubectl_cmd} apply -f {file}'))


def get_kubectl_command(target=None, namespace=None, profile=None):
    cmd = KUBECTL_CMD
    if namespace is not None:
        cmd += f' --namespace {namespace}'
    if target == OCP_TARGET:
        kubeconfig = os.environ.get("OCP_KUBECONFIG")
        if kubeconfig is None:
            kubeconfig = "build/kubeconfig"
        cmd += f' --kubeconfig {kubeconfig}'
        return cmd
    if profile is None or target != LOCAL_TARGET:
        return cmd
    server = get_minikube_server(profile) if target == LOCAL_TARGET else None
    cmd += f' --server https://{server}:8443'
    return cmd


def get_minikube_server(profile):
    p = subprocess.Popen(
        f'minikube ip --profile {profile}',
        shell=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    out = p.stdout.read().decode().strip()
    err = p.stderr.read().decode().strip()
    if err:
        raise RuntimeError(
            f'failed to get minikube ip for profile {profile}: {err}'
        )

    return out

