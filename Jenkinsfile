// Get current date
def now = new Date()

// isTriggerByTimer == true if the timer triggers it
// isEmpty() on lists is currently broken in Jenkins pipelines... so we have to rely on the size of the list
boolean isTriggeredByTimer = currentBuild.getBuildCauses('hudson.triggers.TimerTrigger$TimerTriggerCause').size() != 0

// Generate the cron string based off the branch name
def cronScheduleString(branchName = BRANCH_NAME) {
    String cronScheduleString
    if (isCandidateBranch(branchName)) {
        cronScheduleString = '@daily'
    } else {
        cronScheduleString = ''
    }
    return cronScheduleString
}

// Determine if the branch is a branch we want to create release candidate images from
def isCandidateBranch(branchName = BRANCH_NAME) {
    // List of regex to match branches for release candidate publishing
    def candidateBranches = [/^master$/, /^ocm-\d[.]{1}\d$/]
    return branchName && (candidateBranches.collect { branchName =~ it ? true : false }).contains(true)
}

// Determine the publish tag for the release candidate images
def releaseBranchPublishTag(branchName = BRANCH_NAME) {
    String publish_tag
    if (branchName == 'master') {
        publish_tag = 'latest'
    } else {
        publish_tag = branchName
    }
    return publish_tag
}

pipeline {
  agent { label 'centos_worker' }
  triggers { cron(cronScheduleString(env.BRANCH_NAME)) }
  environment {
        CURRENT_DATE = now.format("Ymd")
        PUBLISH_TAG = releaseBranchPublishTag(env.BRANCH_NAME)

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

    stage('publish release candidate images') {
        when {
            expression { isCandidateBranch(env.BRANCH_NAME) }
        }
        steps {
            sh "make publish PUBLISH_TAG=${PUBLISH_TAG}"

            script {
                if (env.BRANCH_NAME ==~ /^ocm-\d[.]{1}\d$/ && isTriggeredByTimer) {
                    sh "make publish PUBLISH_TAG=${PUBLISH_TAG}-${CURRENT_DATE}"
                }
            }
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
