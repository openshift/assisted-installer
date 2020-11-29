String cron_string = BRANCH_NAME == "master" ? "@daily" : ""

pipeline {
  agent { label 'centos_worker' }
  triggers { cron(cron_string) }
  environment {

        // Credentials
        SLACK_TOKEN = credentials('slack-token')
        QUAY_IO_CREDS = credentials('ocpmetal_cred')

  }
  options {
    timeout(time: 1, unit: 'HOURS')
  }

  stages {

    stage('Init') {
        steps {
            // Login to quay.io
            sh "docker login quay.io -u ${QUAY_IO_CREDS_USR} -p ${QUAY_IO_CREDS_PSW}"
            sh "podman login quay.io -u ${QUAY_IO_CREDS_USR} -p ${QUAY_IO_CREDS_PSW}"
        }
    }

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

    stage('publish images') {
        when {
            expression {!env.BRANCH_NAME.startsWith('PR')}
        }
        steps{
            sh "make publish"
        }
    }

    stage('publish images on push to master') {
        when {
            branch 'master'
        }
        steps {
            sh "make publish PUBLISH_TAG=latest"
        }
    }
  }
  post {
    always {
        script {
           if ((env.BRANCH_NAME == 'master') && (currentBuild.currentResult == "ABORTED" || currentBuild.currentResult == "FAILURE")){
               script {
                   def data = [text: "Attention! ${BUILD_TAG} job failed, see: ${BUILD_URL}"]
                   writeJSON(file: 'data.txt', json: data, pretty: 4)
               }
               sh '''curl -X POST -H 'Content-type: application/json' --data-binary "@data.txt" https://hooks.slack.com/services/${SLACK_TOKEN}'''
           }
        }
    }
  }
}
