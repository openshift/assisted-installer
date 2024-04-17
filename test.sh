NODE_IP=$(cat /tmp/test)

function recert {
  local variablename='\usr\bin\env '
  #myvariable='_Awesome'
  #anothervariable="$variablename"Bash_Is"$myvariable"
  echo ${NODE_IP}
  if [[ -n "${NODE_IP}" ]]; then
     echo "AAAAAAAAAA"
     ggg="aaaa""cn-san-replace 1111:${NODE_IP}"
     variablename="$variablename" "cn-san-replace 1111:${NODE_IP}"
     echo "${NODE_IP}" > /tmp/node-ip
  fi
  echo "$anothervariable"
  echo "$ggg"

  if [[ ${NODE_IP} =~ .*:.* ]]; then
      ETCD_NEW_IP="[${NODE_IP}]"
  else
      ETCD_NEW_IP=${NODE_IP}
  fi
  echo ${ETCD_NEW_IP}
}

recert
