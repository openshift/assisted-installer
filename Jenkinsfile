pipeline {

  environment { IMAGE = 'ocpmetal/assisted-installer'}
  agent {
    node {
      label 'bm-inventory-subsystem'
    }
  }
  stages {
    stage('build') {
      steps {
        sh 'docker build . -f Dockerfile.assisted-installer-build -t ocpmetal/assisted-installer'
      }
    }


  stage('publish images on push to master') {
              when {
                  branch 'master'
              }

              steps {
                  withCredentials([usernamePassword(credentialsId: 'ocpmetal_cred', passwordVariable: 'PASS', usernameVariable: 'USER')]) {
                      sh '''docker login quay.io -u $USER -p $PASS'''
                  }

                  sh '''docker tag  ${IMAGE} quay.io/ocpmetal/assisted-installer:latest'''
                  sh '''docker tag  ${IMAGE} quay.io/ocpmetal/assisted-installer:${GIT_COMMIT}'''
                  sh '''docker push quay.io/ocpmetal/assisted-installer:latest'''
                  sh '''docker push quay.io/ocpmetal/assisted-installer:${GIT_COMMIT}'''
              }
   }
}
}