String cron_string = BRANCH_NAME == "master" ? "@daily" : ""

pipeline {
  agent { label 'centos_worker' }
  triggers { cron(cron_string) }
  environment {
        INSTALLER = 'quay.io/ocpmetal/assisted-installer'
        CONTROLLER = 'quay.io/ocpmetal/assisted-installer-controller'
        SLACK_TOKEN = credentials('slack-token')
        MASTER_SLACK_TOKEN = credentials('slack_master_token')
  }
  options {
    timeout(time: 1, unit: 'HOURS')
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
                    script {
                        def data = [text: "Attention! assisted-installer branch  test failed, see: ${BUILD_URL}"]
                        writeJSON(file: 'data.txt', json: data, pretty: 4)
                    }
                    sh '''curl -X POST -H 'Content-type: application/json' --data-binary "@data.txt"  https://hooks.slack.com/services/$MASTER_SLACK_TOKEN'''
                }
        }
    }
  }
}
