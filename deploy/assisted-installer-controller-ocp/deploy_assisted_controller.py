import os
import utils
import argparse

log = utils.get_logger('deploy_assisted_controller')

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--inventory-url")
    parser.add_argument("--cluster-id")
    parser.add_argument("--controller-image")
    parser.add_argument("--namespace")
    parser.add_argument("--target")
    parser.add_argument("--profile")
    deploy_options = parser.parse_args()

    log.info('Starting assisted-installer-controller deployment')
    utils.verify_build_directory(deploy_options.namespace)
    deploy_controller(deploy_options)
    log.info('Completed assisted-installer-controller deployment')

def deploy_controller(deploy_options):
    src_file = os.path.join(os.getcwd(), 'deploy/assisted-installer-controller-ocp/assisted-controller-deployment.yaml')
    dst_file = os.path.join(os.getcwd(), 'build', deploy_options.namespace, 'assisted-controller-deployment.yaml')
    with open(src_file, "r") as src:
        with open(dst_file, "w+") as dst:
            data = src.read()
            data = data.replace('REPLACE_CONTROLLER_OCP_IMAGE', f'"{deploy_options.controller_image}"')
            data = data.replace('REPLACE_INVENTORY_URL', f'"{deploy_options.inventory_url}"')
            data = data.replace('REPLACE_CLUSTER_ID', f'"{deploy_options.cluster_id}"')
            data = data.replace('REPLACE_NAMESPACE', f'"{deploy_options.namespace}"')
            print("Deploying {}".format(dst_file))
            dst.write(data)

    log.info('Deploying %s', dst_file)
    utils.apply(
        target=deploy_options.target,
        namespace=deploy_options.namespace,
        profile=deploy_options.profile,
        file=dst_file
    )

if __name__ == "__main__":
    main()
