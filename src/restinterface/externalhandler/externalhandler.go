/*******************************************************************************
 * Copyright 2019 Samsung Electronics All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 *******************************************************************************/

// Package externalhandler implements REST server functions to communication between orchestration and service applications
package externalhandler

import (
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"

	"common/networkhelper"
	"db/bolt/common"
	"orchestrationapi"
	"restinterface"
	"restinterface/cipher"
	"restinterface/externalhandler/senderresolver"
	"restinterface/resthelper"
	"controller/securemgr"
)

const logPrefix = "RestExternalInterface"

// Handler struct
type Handler struct {
	isSetAPI bool
	api      orchestrationapi.OrcheExternalAPI

	isSetSecureAPI bool
	sapi     securemgr.SecureMgrExternalAPI

	helper resthelper.RestHelper

	restinterface.HasRoutes
	cipher.HasCipher

	netHelper networkhelper.Network
}

var handler *Handler

func init() {
	handler = new(Handler)
	handler.helper = resthelper.GetHelper()
	handler.Routes = restinterface.Routes{

		restinterface.Route{
			Name:        "APIV1RequestServicePost",
			Method:      strings.ToUpper("Post"),
			Pattern:     "/api/v1/orchestration/services",
			HandlerFunc: handler.APIV1RequestServicePost,
		},
		restinterface.Route{
			Name:        "APIV1RequestSecuremgrPost",
			Method:      strings.ToUpper("Post"),
			Pattern:     "/api/v1/orchestration/securemgr",
			HandlerFunc: handler.APIV1RequestSecuremgrPost,
		},

	}
	handler.netHelper = networkhelper.GetInstance()
}

// GetHandler returns the singleton Handler instance
func GetHandler() *Handler {
	return handler
}

// SetOrchestrationAPI sets OrcheExternalAPI
func (h *Handler) SetOrchestrationAPI(o orchestrationapi.OrcheExternalAPI) {
	h.api = o
	h.isSetAPI = true
}

// SetSecuremgrAPI sets SecureMgrExternalAPI
func (h *Handler) SetSecuremgrAPI(s securemgr.SecureMgrExternalAPI) {
	h.sapi = s
	h.isSetSecureAPI = true
}

// APIV1RequestServicePost handles service request from service application
func (h *Handler) APIV1RequestServicePost(w http.ResponseWriter, r *http.Request) {
	log.Printf("[%s] APIV1RequestServicePost", logPrefix)
	if h.isSetAPI == false {
		log.Printf("[%s] does not set api", logPrefix)
		h.helper.Response(w, http.StatusServiceUnavailable)
		return
	} else if h.IsSetKey == false {
		log.Printf("[%s] does not set key", logPrefix)
		h.helper.Response(w, http.StatusServiceUnavailable)
		return
	}

	reqAddr := strings.Split(r.RemoteAddr, ":")
	var addr string
	var portStr string
	if strings.Contains(r.RemoteAddr, "::1") {
		addr = "localhost"
		portStr = reqAddr[len(reqAddr)-1]
	} else {
		addr = reqAddr[0]
		portStr = reqAddr[1]
	}

	ips, err := h.netHelper.GetIPs()
	if err != nil {
		h.helper.Response(w, http.StatusServiceUnavailable)
		return
	} else if addr != "localhost" && addr != "127.0.0.1" && common.HasElem(ips, addr) == false {
		h.helper.Response(w, http.StatusNotAcceptable)
		return
	}

	var (
		responseMsg  string
		responseName string
		resp         orchestrationapi.ResponseService

		name               string
		executeEnvs        []interface{}
		responseTargetInfo map[string]interface{}
	)

	//request
	encryptBytes, _ := ioutil.ReadAll(r.Body)

	appCommand, err := h.Key.DecryptByteToJSON(encryptBytes)
	if err != nil {
		log.Printf("[%s] can not decryption", logPrefix)
		h.helper.Response(w, http.StatusServiceUnavailable)
		return
	}

	serviceInfos := orchestrationapi.ReqeustService{}
	selfSelection, ok := appCommand["SelfSelection"].(string)
	if !ok {
		selfSelection = "true"
	}
	if selfSelection == "true" {
		serviceInfos.SelfSelection = true
	} else {
		serviceInfos.SelfSelection = false
	}

	isParseRequesterFromPort := true
	port, err := strconv.Atoi(portStr)
	log.Println("port: ", port)
	if err != nil {
		isParseRequesterFromPort = false
	} else {
		requester, err := senderresolver.GetNameByPort(int64(port))
		log.Println("requester: ", requester)
		if err != nil {
			isParseRequesterFromPort = false
		} else {
			serviceInfos.ServiceRequester = requester
		}
	}

	if isParseRequesterFromPort != true {
		serviceRequester, ok := appCommand["ServiceRequester"].(string)
		if !ok {
			responseMsg = orchestrationapi.INVALID_PARAMETER
			responseName = ""
			goto SEND_RESP
		}
		serviceInfos.ServiceRequester = serviceRequester
	}

	name, ok = appCommand["ServiceName"].(string)
	if !ok {
		responseMsg = orchestrationapi.INVALID_PARAMETER
		responseName = ""
		goto SEND_RESP
	}
	serviceInfos.ServiceName = name

	executeEnvs, ok = appCommand["ServiceInfo"].([]interface{})
	if !ok {
		responseMsg = orchestrationapi.INVALID_PARAMETER
		responseName = name
		goto SEND_RESP
	}

	serviceInfos.ServiceInfo = make([]orchestrationapi.RequestServiceInfo, len(executeEnvs))
	for idx, executeEnv := range executeEnvs {
		tmp := executeEnv.(map[string]interface{})
		exeType, ok := tmp["ExecutionType"].(string)
		if !ok {
			responseMsg = orchestrationapi.INVALID_PARAMETER
			responseName = name
			goto SEND_RESP
		}
		serviceInfos.ServiceInfo[idx].ExecutionType = exeType

		exeCmd, ok := tmp["ExecCmd"].([]interface{})
		if !ok {
			responseMsg = orchestrationapi.INVALID_PARAMETER
			responseName = name
			goto SEND_RESP
		}

		serviceInfos.ServiceInfo[idx].ExeCmd = make([]string, len(exeCmd))
		for idy, cmd := range exeCmd {
			serviceInfos.ServiceInfo[idx].ExeCmd[idy] = cmd.(string)
		}
	}

	resp = h.api.RequestService(serviceInfos)

	responseMsg = resp.Message
	responseName = resp.ServiceName

	responseTargetInfo = make(map[string]interface{})
	responseTargetInfo["ExecutionType"] = resp.RemoteTargetInfo.ExecutionType
	responseTargetInfo["Target"] = resp.RemoteTargetInfo.Target

SEND_RESP:
	respJSONMsg := make(map[string]interface{})
	respJSONMsg["Message"] = responseMsg
	respJSONMsg["ServiceName"] = responseName
	respJSONMsg["RemoteTargetInfo"] = responseTargetInfo

	respEncryptBytes, err := h.Key.EncryptJSONToByte(respJSONMsg)
	if err != nil {
		log.Printf("[%s] can not encryption", logPrefix)
		h.helper.Response(w, http.StatusServiceUnavailable)
		return
	}

	h.helper.ResponseJSON(w, respEncryptBytes, http.StatusOK)
}

// APIV1RequestSecuremgrPost handles securemgr request from securemgr configure application
func (h *Handler) APIV1RequestSecuremgrPost(w http.ResponseWriter, r *http.Request) {
	log.Printf("[%s] APIV1RequestSecuremgrPost", logPrefix)
	if h.isSetSecureAPI == false {
		log.Printf("[%s] does not set api", logPrefix)
		h.helper.Response(w, http.StatusServiceUnavailable)
		return
	} else if h.IsSetKey == false {
		log.Printf("[%s] does not set key", logPrefix)
		h.helper.Response(w, http.StatusServiceUnavailable)
		return
	}

	reqAddr := strings.Split(r.RemoteAddr, ":")
	var addr string
	if strings.Contains(r.RemoteAddr, "::1") {
		addr = "localhost"
	} else {
		addr = reqAddr[0]
	}

	ips, err := h.netHelper.GetIPs()
	if err != nil {
		h.helper.Response(w, http.StatusServiceUnavailable)
		return
	} else if addr != "localhost" && addr != "127.0.0.1" && common.HasElem(ips, addr) == false {
		h.helper.Response(w, http.StatusNotAcceptable)
		return
	}

	var (
		responseMsg  string
		responseName string
		resp         securemgr.ResponseSecureMgr
		containerDescs        []interface{}
	)

	//request
	encryptBytes, _ := ioutil.ReadAll(r.Body)

	appCommand, err := h.Key.DecryptByteToJSON(encryptBytes)
	if err != nil {
		log.Printf("[%s] cannot decryption", logPrefix)
		h.helper.Response(w, http.StatusServiceUnavailable)
		return
	}

	containerInfos := securemgr.RequestSecureMgr{}

	SecureInsName, ok := appCommand["SecureMgr"].(string)
	if ok {
		containerInfos.SecureInsName = SecureInsName
		log.Println("SecureMgr: ", containerInfos.SecureInsName)
	}

	containerInfos.CmdType, ok = appCommand["CmdType"].(string)
	if ok {
		log.Println("CmdType: ", containerInfos.CmdType)
	}
	if containerInfos.CmdType == "addHashCWL" || containerInfos.CmdType =="delHashCWL" {
		containerDescs, ok = appCommand["Desc"].([]interface{})
		if !ok {
			log.Println("Error")
			responseMsg = securemgr.INVALID_PARAMETER
			responseName = "verifier"
			goto SEND_RESP
		}

		containerInfos.Desc = make([]securemgr.RequestDescInfo, len(containerDescs))
		for idx, containerDesc := range containerDescs {
			tmp := containerDesc.(map[string]interface{})
			//name, ok := tmp["ContainerName"].(string)
			//if !ok {
			//	responseMsg = securemgr.INVALID_PARAMETER
			//	responseName = "verifier"
			//	goto SEND_RESP
			//}
			//containerInfos.Desc[idx].ContainerName = name

			hash, ok := tmp["ContainerHash"].(string)
			if !ok {
				responseMsg = securemgr.INVALID_PARAMETER
				responseName = "verifier"
				goto SEND_RESP
			}
			containerInfos.Desc[idx].ContainerHash = hash
		}
	}

	resp = h.sapi.RequestSecureMgr(containerInfos)

	responseMsg = resp.Message
	responseName = resp.SecureCmpName

SEND_RESP:
	respJSONMsg := make(map[string]interface{})
	respJSONMsg["Message"] = responseMsg
	respJSONMsg["SecureCmpName"] = responseName

	respEncryptBytes, err := h.Key.EncryptJSONToByte(respJSONMsg)
	if err != nil {
		log.Printf("[%s] cannot encryption", logPrefix)
		h.helper.Response(w, http.StatusServiceUnavailable)
		return
	}

	h.helper.ResponseJSON(w, respEncryptBytes, http.StatusOK)
}

func (h *Handler) setHelper(helper resthelper.RestHelper) {
	h.helper = helper
}
