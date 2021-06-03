String cron_string = BRANCH_NAME == "master" ? "@daily" : ""

pipeline {
  agent { label 'centos_worker' }
  triggers { cron(cron_string) }
  environment {
        CI="true"

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
            script {
                for (tool in ["docker", "podman"]) {
                    for (repo_details in [["quay.io", "${QUAY_IO_CREDS_USR}", "${QUAY_IO_CREDS_PSW}"]]) {
                        (repo, user, pass) = repo_details
                        sh "${tool} login ${repo} -u ${user} -p ${pass}"
                    }
                }
            }
        }
    }

    stage('build') {
        steps {
            sh 'skipper make'
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

            junit '**/reports/junit*.xml'
            cobertura coberturaReportFile: '**/reports/*coverage.xml', onlyStable: false, enableNewApi: true
        }
    }
  }
}
