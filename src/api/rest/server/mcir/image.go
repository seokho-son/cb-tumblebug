/*
Copyright 2019 The Cloud-Barista Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package mcir is to handle REST API for mcir
package mcir

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cloud-barista/cb-tumblebug/src/core/common"
	"github.com/cloud-barista/cb-tumblebug/src/core/mcir"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
)

// RestPostImage godoc
// @ID PostImage
// @Summary Register image
// @Description Register image
// @Tags [Infra resource] MCIR Image management
// @Accept  json
// @Produce  json
// @Param action query string true "registeringMethod" Enums(registerWithInfo, registerWithId)
// @Param nsId path string true "Namespace ID" default(system-purpose-common-ns)
// @Param imageInfo body mcir.TbImageInfo false "Specify details of a image object (cspImageName, guestOS, description, ...) manually"
// @Param imageId body mcir.TbImageReq false "Specify name, connectionName and cspImageId to register an image object automatically"
// @Param update query boolean false "Force update to existing image object" default(false)
// @Success 200 {object} mcir.TbImageInfo
// @Failure 404 {object} common.SimpleMsg
// @Failure 500 {object} common.SimpleMsg
// @Router /ns/{nsId}/resources/image [post]
func RestPostImage(c echo.Context) error {
	reqID, idErr := common.StartRequestWithLog(c)
	if idErr != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": idErr.Error()})
	}
	nsId := c.Param("nsId")

	action := c.QueryParam("action")
	updateStr := c.QueryParam("update")
	update, err := strconv.ParseBool(updateStr)
	if err != nil {
		update = false
	}

	log.Debug().Msg("[POST Image] (action: " + action + ")")
	/*
		if action == "create" {
			log.Debug().Msg("[Creating Image]")
			content, _ := createImage(nsId, u)
			return c.JSON(http.StatusCreated, content)

		} else */
	if action == "registerWithInfo" {
		log.Debug().Msg("[Registering Image with info]")
		u := &mcir.TbImageInfo{}
		if err := c.Bind(u); err != nil {
			return common.EndRequestWithLog(c, reqID, err, nil)
		}
		content, err := mcir.RegisterImageWithInfo(nsId, u, update)
		return common.EndRequestWithLog(c, reqID, err, content)
	} else if action == "registerWithId" {
		log.Debug().Msg("[Registering Image with ID]")
		u := &mcir.TbImageReq{}
		if err := c.Bind(u); err != nil {
			return common.EndRequestWithLog(c, reqID, err, nil)
		}
		content, err := mcir.RegisterImageWithId(nsId, u, update)
		return common.EndRequestWithLog(c, reqID, err, content)
	} else {
		err := fmt.Errorf("You must specify: action=registerWithInfo or action=registerWithId")
		return common.EndRequestWithLog(c, reqID, err, nil)
	}

}

// RestPutImage godoc
// @ID PutImage
// @Summary Update image
// @Description Update image
// @Tags [Infra resource] MCIR Image management
// @Accept  json
// @Produce  json
// @Param imageInfo body mcir.TbImageInfo true "Details for an image object"
// @Param nsId path string true "Namespace ID" default(ns01)
// @Param imageId path string true "Image ID ({providerName}+{regionName}+{imageName})"
// @Success 200 {object} mcir.TbImageInfo
// @Failure 404 {object} common.SimpleMsg
// @Failure 500 {object} common.SimpleMsg
// @Router /ns/{nsId}/resources/image/{imageId} [put]
func RestPutImage(c echo.Context) error {
	reqID, idErr := common.StartRequestWithLog(c)
	if idErr != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": idErr.Error()})
	}
	nsId := c.Param("nsId")
	resourceId := c.Param("imageId")
	resourceId = strings.ReplaceAll(resourceId, " ", "+")
	resourceId = strings.ReplaceAll(resourceId, "%2B", "+")

	u := &mcir.TbImageInfo{}
	if err := c.Bind(u); err != nil {
		return common.EndRequestWithLog(c, reqID, err, nil)
	}

	content, err := mcir.UpdateImage(nsId, resourceId, *u)
	return common.EndRequestWithLog(c, reqID, err, content)
}

// Request structure for RestLookupImage
type RestLookupImageRequest struct {
	ConnectionName string `json:"connectionName"`
	CspImageId     string `json:"cspImageId"`
}

// RestLookupImage godoc
// @ID LookupImage
// @Summary Lookup image
// @Description Lookup image
// @Tags [Infra resource] MCIR Common
// @Accept  json
// @Produce  json
// @Param lookupImageReq body RestLookupImageRequest true "Specify connectionName & cspImageId"
// @Success 200 {object} mcir.SpiderImageInfo
// @Failure 404 {object} common.SimpleMsg
// @Failure 500 {object} common.SimpleMsg
// @Router /lookupImage [post]
func RestLookupImage(c echo.Context) error {
	reqID, idErr := common.StartRequestWithLog(c)
	if idErr != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": idErr.Error()})
	}
	u := &RestLookupImageRequest{}
	if err := c.Bind(u); err != nil {
		return common.EndRequestWithLog(c, reqID, err, nil)
	}

	log.Debug().Msg("[Lookup image]: " + u.CspImageId)
	content, err := mcir.LookupImage(u.ConnectionName, u.CspImageId)
	return common.EndRequestWithLog(c, reqID, err, content)

}

// RestLookupImageList godoc
// @ID LookupImageList
// @Summary Lookup image list
// @Description Lookup image list
// @Tags [Infra resource] MCIR Common
// @Accept  json
// @Produce  json
// @Param lookupImagesReq body common.TbConnectionName true "Specify connectionName"
// @Success 200 {object} mcir.SpiderImageList
// @Failure 404 {object} common.SimpleMsg
// @Failure 500 {object} common.SimpleMsg
// @Router /lookupImages [post]
func RestLookupImageList(c echo.Context) error {
	reqID, idErr := common.StartRequestWithLog(c)
	if idErr != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": idErr.Error()})
	}
	u := &RestLookupImageRequest{}
	if err := c.Bind(u); err != nil {
		return common.EndRequestWithLog(c, reqID, err, nil)
	}

	log.Debug().Msg("[Lookup images]")
	content, err := mcir.LookupImageList(u.ConnectionName)
	return common.EndRequestWithLog(c, reqID, err, content)

}

// RestFetchImages godoc
// @ID FetchImages
// @Summary Fetch images
// @Description Fetch images
// @Tags [Infra resource] MCIR Image management
// @Accept  json
// @Produce  json
// @Param nsId path string true "Namespace ID" default(ns01)
// @Success 200 {object} common.SimpleMsg
// @Failure 404 {object} common.SimpleMsg
// @Failure 500 {object} common.SimpleMsg
// @Router /ns/{nsId}/resources/fetchImages [post]
func RestFetchImages(c echo.Context) error {
	reqID, idErr := common.StartRequestWithLog(c)
	if idErr != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": idErr.Error()})
	}
	nsId := c.Param("nsId")

	u := &RestLookupImageRequest{}
	if err := c.Bind(u); err != nil {
		return common.EndRequestWithLog(c, reqID, err, nil)
	}

	var connConfigCount, imageCount uint
	var err error

	if u.ConnectionName == "" {
		connConfigCount, imageCount, err = mcir.FetchImagesForAllConnConfigs(nsId)
		if err != nil {
			return common.EndRequestWithLog(c, reqID, err, nil)
		}
	} else {
		connConfigCount = 1
		imageCount, err = mcir.FetchImagesForConnConfig(u.ConnectionName, nsId)
		if err != nil {
			return common.EndRequestWithLog(c, reqID, err, nil)
		}
	}

	content := map[string]string{
		"message": "Fetched " + fmt.Sprint(imageCount) + " images (from " + fmt.Sprint(connConfigCount) + " connConfigs)"}
	return common.EndRequestWithLog(c, reqID, err, content)
}

// RestGetImage godoc
// @ID GetImage
// @Summary Get image
// @Description Get image
// @Tags [Infra resource] MCIR Image management
// @Accept  json
// @Produce  json
// @Param nsId path string true "Namespace ID" default(ns01)
// @Param imageId path string true "Image ID ({providerName}+{regionName}+{imageName})"
// @Success 200 {object} mcir.TbImageInfo
// @Failure 404 {object} common.SimpleMsg
// @Failure 500 {object} common.SimpleMsg
// @Router /ns/{nsId}/resources/image/{imageId} [get]
func RestGetImage(c echo.Context) error {
	// This is a dummy function for Swagger.
	return nil
}

// Response structure for RestGetAllImage
type RestGetAllImageResponse struct {
	Image []mcir.TbImageInfo `json:"image"`
}

// RestGetAllImage godoc
// @ID GetAllImage
// @Summary List all images or images' ID
// @Description List all images or images' ID
// @Tags [Infra resource] MCIR Image management
// @Accept  json
// @Produce  json
// @Param nsId path string true "Namespace ID" default(ns01)
// @Param option query string false "Option" Enums(id)
// @Param filterKey query string false "Field key for filtering (ex:guestOS)"
// @Param filterVal query string false "Field value for filtering (ex: Ubuntu18.04)"
// @Success 200 {object} JSONResult{[DEFAULT]=RestGetAllImageResponse,[ID]=common.IdList} "Different return structures by the given option param"
// @Failure 404 {object} common.SimpleMsg
// @Failure 500 {object} common.SimpleMsg
// @Router /ns/{nsId}/resources/image [get]
func RestGetAllImage(c echo.Context) error {
	// This is a dummy function for Swagger.
	return nil
}

// RestDelImage godoc
// @ID DelImage
// @Summary Delete image
// @Description Delete image
// @Tags [Infra resource] MCIR Image management
// @Accept  json
// @Produce  json
// @Param nsId path string true "Namespace ID" default(ns01)
// @Param imageId path string true "Image ID ({providerName}+{regionName}+{imageName})"
// @Success 200 {object} common.SimpleMsg
// @Failure 404 {object} common.SimpleMsg
// @Router /ns/{nsId}/resources/image/{imageId} [delete]
func RestDelImage(c echo.Context) error {
	// This is a dummy function for Swagger.
	return nil
}

// RestDelAllImage godoc
// @ID DelAllImage
// @Summary Delete all images
// @Description Delete all images
// @Tags [Infra resource] MCIR Image management
// @Accept  json
// @Produce  json
// @Param nsId path string true "Namespace ID" default(ns01)
// @Param match query string false "Delete resources containing matched ID-substring only" default()
// @Success 200 {object} common.IdList
// @Failure 404 {object} common.SimpleMsg
// @Router /ns/{nsId}/resources/image [delete]
func RestDelAllImage(c echo.Context) error {
	// This is a dummy function for Swagger.
	return nil
}

// Response structure for RestSearchImage
type RestSearchImageRequest struct {
	Keywords []string `json:"keywords"`
}

// RestSearchImage godoc
// @ID SearchImage
// @Summary Search image
// @Description Search image
// @Tags [Infra resource] MCIR Image management
// @Accept  json
// @Produce  json
// @Param nsId path string true "Namespace ID" default(ns01)
// @Param keywords body RestSearchImageRequest true "Keywords"
// @Success 200 {object} RestGetAllImageResponse
// @Failure 404 {object} common.SimpleMsg
// @Failure 500 {object} common.SimpleMsg
// @Router /ns/{nsId}/resources/searchImage [post]
func RestSearchImage(c echo.Context) error {
	reqID, idErr := common.StartRequestWithLog(c)
	if idErr != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": idErr.Error()})
	}
	nsId := c.Param("nsId")

	u := &RestSearchImageRequest{}
	if err := c.Bind(u); err != nil {
		return err
	}

	content, err := mcir.SearchImage(nsId, u.Keywords...)
	result := RestGetAllImageResponse{}
	result.Image = content
	return common.EndRequestWithLog(c, reqID, err, result)
}
