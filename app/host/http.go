package main

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/tez-capital/tezsign/broker"
	"github.com/tez-capital/tezsign/common"
	"github.com/tez-capital/tezsign/signer"
)

type signReq struct {
	KeyID      string `json:"key_id"`
	PayloadHex string `json:"payload_hex"`
}
type signResp struct {
	Signature string `json:"signature"`
}

func findStatusByTz4(st *signer.StatusResponse, id string) *signer.KeyStatus {
	if st == nil {
		return nil
	}
	for _, k := range st.GetKeys() {
		if k.GetTz4() == id {
			return k
		}
	}
	return nil
}

func buildFiberApp(getB func() *broker.Broker, l *slog.Logger, allowedTZ4 map[string]struct{}) *fiber.App {
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          10 * time.Second,
		IdleTimeout:           60 * time.Second,

		// BodyLimit: 1<<20, // 1MB; uncomment if you want a hard cap
	})

	// Middlewares: recover from panics + compact request log
	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		// Keep logs short; you already have slog for app logs.
		Format: "${time} ${method} ${path} ${status} ${latency}\n",
	}))
	app.Use(func(c *fiber.Ctx) error {
		c.Path(path.Clean(c.Path()))
		return c.Next()
	})

	// -------------------------------------------------------------------------
	// GET /authorized_keys
	// DO NOT TOUCH - octez wants it like this
	// -------------------------------------------------------------------------
	app.Get("/authorized_keys", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{})
	})

	// -------------------------------------------------------------------------
	// GET /keys/:tz4 → return {"public_key":"BLpk..."}
	// -------------------------------------------------------------------------
	app.Get("/keys/:tz4", func(c *fiber.Ctx) error {
		tz4 := c.Params("tz4")
		if tz4 == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing PKH"})
		}

		st, err := common.ReqStatus(getB())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}

		if _, ok := allowedTZ4[tz4]; !ok {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "key not found"})
		}

		ks := findStatusByTz4(st, tz4)
		if ks == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "key not found"})
		}

		return c.JSON(fiber.Map{"public_key": ks.GetBlPubkey()})
	})

	// -------------------------------------------------------------------------
	// POST /keys/:tz4 → return {"signature":"BLsig..."}
	// -------------------------------------------------------------------------
	app.Post("/keys/:tz4", func(c *fiber.Ctx) error {
		tz4 := c.Params("tz4")
		if tz4 == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing PKH"})
		}

		var payloadHex string
		if err := c.BodyParser(&payloadHex); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		raw, err := hex.DecodeString(payloadHex)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("bad payload_hex: %v", err)})
		}

		if _, ok := allowedTZ4[tz4]; !ok {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "key not found"})
		}

		sig, err := common.ReqSign(getB(), tz4, raw)
		if err != nil {
			if re, ok := err.(*common.RemoteError); ok {
				switch re.Code {
				case common.RpcKeyNotFound:
					return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": re.Msg})
				case common.RpcKeyLocked:
					return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": re.Msg})
				case common.RpcStaleWatermark:
					return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": re.Msg})
				case common.RpcBadPayload:
					return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": re.Msg})
				default:
					return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": re.Msg})
				}
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}

		blSig, err := signer.EncodeBLSignature(sig)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}

		return c.JSON(&signResp{Signature: blSig})
	})

	// -------------------------------------------------------------------------
	// POST /sign → sign payloads
	// -------------------------------------------------------------------------
	app.Post("/sign", func(c *fiber.Ctx) error {
		// TODO: remove if not needed
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{"error": "not implemented"})

		// var req signReq
		// if err := c.BodyParser(&req); err != nil {
		// 	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		// }
		// if req.KeyID == "" || req.PayloadHex == "" {
		// 	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "key_id and payload_hex required"})
		// }

		// // 402 if not in allow-list (don’t leak existence)
		// if _, ok := allowedSet[req.KeyID]; !ok {
		// 	return c.Status(402).JSON(fiber.Map{"error": "key not found"})
		// }

		// // Distinguish 403 (locked) vs 404 (unknown on device)
		// st, err := common.ReqStatus(getB())
		// if err != nil {
		// 	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		// }
		// ks := findKeyStatus(st, req.KeyID)
		// if ks == nil {
		// 	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "key not found"})
		// }
		// if ks.GetLockState() != signer.LockState_UNLOCKED {
		// 	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "key locked"})
		// }

		// raw, err := hex.DecodeString(req.PayloadHex)
		// if err != nil {
		// 	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("bad payload_hex: %v", err)})
		// }
		// sig, lvl, rnd, err := common.ReqSign(getB(), req.KeyID, raw)
		// if err != nil {
		// 	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		// }
		// blsig, err := signer.EncodeBLSignature(sig)
		// if err != nil {
		// 	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		// }
		// return c.JSON(&signResp{BLSig: blsig, Level: lvl, Round: rnd})
	})

	return app
}
