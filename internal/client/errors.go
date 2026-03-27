// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Errors for Redis client connection handling.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package client

import "errors"

// ErrQuit signals the server should close the connection after replying to QUIT.
var ErrQuit = errors.New("redis quit")
