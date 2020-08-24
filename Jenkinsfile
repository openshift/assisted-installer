pipeline {
  environment {
        INSTALLER = 'quay.io/ocpmetal/assisted-installer'
        CONTROLLER = 'quay.io/ocpmetal/assisted-installer-controller'
  }
  agent {
    node {
        label 'centos_worker'
    }
  }
  stages {
    stage('build') {
        steps {
            sh 'skipper make'
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
            sh '''docker tag  ${INSTALLER} ${INSTALLER}:latest'''
            sh '''docker tag  ${INSTALLER} ${INSTALLER}:${GIT_COMMIT}'''
            sh '''docker push ${INSTALLER}:latest'''
            sh '''docker push ${INSTALLER}:${GIT_COMMIT}'''

            sh '''docker tag  ${CONTROLLER} ${CONTROLLER}:latest'''
            sh '''docker tag  ${CONTROLLER} ${CONTROLLER}:${GIT_COMMIT}'''
            sh '''docker push ${CONTROLLER}:latest'''
            sh '''docker push ${CONTROLLER}:${GIT_COMMIT}'''
        }
    }
  }
  post {
            failure {
                script {
                    if (env.BRANCH_NAME == 'master')
                        stage('notify master branch fail') {
                            withCredentials([string(credentialsId: 'slack-token', variable: 'TOKEN')]) {
                               echo '{"text":"Attention! assisted-installer master branch subsystem test failed, see: ' > data.txt
                               echo ${BUILD_URL} >> data.txt
                               echo '"}' >> data.txt
                            }
                        }
                }
            }
  }
}