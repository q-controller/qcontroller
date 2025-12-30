package images

import (
	"fmt"
	"strings"
)

const openAPITemplate = `%s:
  post:
    tags:
      - %s
    summary: Upload image
    description: Uploads an image file with an associated ID
    requestBody:
      required: true
      content:
        multipart/form-data:
          schema:
            type: object
            properties:
              id:
                type: string
                description: Unique identifier for the image
              file:
                type: string
                format: binary
                description: The image file to upload
            required:
              - id
              - file
    responses:
      '200':
        description: Upload successful
      '400':
        description: Bad request - missing parameters or invalid file
      '500':
        description: Internal server error
  get:
    tags:
      - %s
    summary: List images
    description: Retrieves a list of all available images
    responses:
      '200':
        description: List of images retrieved successfully
        content:
          application/json:
            schema:
              type: object
              properties:
                images:
                  type: array
                  items:
                    type: object
                    properties:
                      image_id:
                        type: string
                        description: Unique identifier for the image
                      hash:
                        type: string
                        description: SHA256 hash of the image
                      size:
                        type: integer
                        format: int64
                        description: Size of the image in bytes
                      uploaded_at:
                        type: string
                        format: date-time
                        description: Timestamp when the image was uploaded
                    required:
                      - image_id
                      - hash
                      - size
                      - uploaded_at
                  description: Array of image metadata objects
      '500':
        description: Internal server error
%s/{imageId}:
  delete:
    tags:
      - %s
    summary: Delete image
    description: Removes an image by its ID
    parameters:
      - name: imageId
        in: path
        required: true
        schema:
          type: string
        description: The ID of the image to delete
    responses:
      '200':
        description: Image deleted successfully
      '400':
        description: Bad request - missing or invalid imageId
      '500':
        description: Internal server error`

func GetOpenAPISpec(rootPath, tag string) string {
	if rootPath == "" || tag == "" {
		return ""
	}

	// Ensure rootPath doesn't have trailing slash
	rootPath = strings.TrimSuffix(rootPath, "/")

	return fmt.Sprintf(openAPITemplate, rootPath, tag, tag, rootPath, tag)
}
