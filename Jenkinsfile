String cron_string = BRANCH_NAME == "master" ? "@daily" : ""

pipeline {
  agent { label 'centos_worker' }
  triggers { cron(cron_string) }
  environment {
        INSTALLER = 'quay.io/ocpmetal/assisted-installer'
        CONTROLLER = 'quay.io/ocpmetal/assisted-installer-controller'
  }
  options {
    timeout(time: 1, unit: 'HOURS')
  }

  stages {
    stage('build') {
        steps {
            sh 'skipper make'
        }
        post {
            always {
                junit '**/reports/*test.xml'
                cobertura coberturaReportFile: '**/reports/*coverage.xml', onlyStable: false, enableNewApi: true
            }
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
    always {
        script {
           if ((env.BRANCH_NAME == 'master') && (currentBuild.currentResult == "ABORTED" || currentBuild.currentResult == "FAILURE")){
                stage('notify master branch fail') {
                    withCredentials([string(credentialsId: 'slack-token', variable: 'TOKEN')]) {
                        sh '''
                        echo '{"text":"Attention! assisted-installer master branch subsystem test failed, see: ' > data.txt
                        echo ${BUILD_URL} >> data.txt
                        echo '"}' >> data.txt
                        curl -X POST -H 'Content-type: application/json' --data-binary "@data.txt"  https://hooks.slack.com/services/$TOKEN
                        '''

                    }
                }
           }
        }
    }
  }
}
