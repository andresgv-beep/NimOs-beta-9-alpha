// rcon.go — Cliente RCON nativo (protocolo Source RCON, el que usa Minecraft).
//
// Implementación pura con la stdlib (net + encoding/binary), SIN dependencias
// externas · coherente con la filosofía minimalista de deps de NimOS.
//
// Protocolo Source RCON · cada paquete es:
//   [Size int32 LE][ID int32 LE][Type int32 LE][Body string null-term][null pad]
//
// Tipos de paquete:
//   3 = SERVERDATA_AUTH          (login con password)
//   2 = SERVERDATA_AUTH_RESPONSE / SERVERDATA_EXECCOMMAND
//   0 = SERVERDATA_RESPONSE_VALUE (respuesta a un comando)
//
// Uso:
//   resp, err := rconExecute("127.0.0.1", 25575, "password", "list", 5*time.Second)
package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	rconTypeAuth         = 3
	rconTypeAuthResponse = 2
	rconTypeExecCommand  = 2
	rconTypeResponse     = 0

	rconAuthFailID = -1 // el server pone ID=-1 si el auth falla

	rconMaxBodySize = 4096 // límite razonable de body por paquete
)

var (
	errRconAuthFailed = errors.New("rcon: autenticación fallida (password incorrecta)")
	errRconBadPacket  = errors.New("rcon: paquete malformado")
)

// rconBuildPacket serializa un paquete RCON. Pura y testeable.
func rconBuildPacket(id, typ int32, body string) []byte {
	bodyBytes := []byte(body)
	// Size = 4 (ID) + 4 (Type) + len(body) + 1 (null del body) + 1 (null pad)
	size := int32(4 + 4 + len(bodyBytes) + 2)

	buf := make([]byte, 0, 4+size)
	tmp := make([]byte, 4)

	binary.LittleEndian.PutUint32(tmp, uint32(size))
	buf = append(buf, tmp...)
	binary.LittleEndian.PutUint32(tmp, uint32(id))
	buf = append(buf, tmp...)
	binary.LittleEndian.PutUint32(tmp, uint32(typ))
	buf = append(buf, tmp...)
	buf = append(buf, bodyBytes...)
	buf = append(buf, 0x00, 0x00) // null del body + null pad

	return buf
}

// rconReadPacket lee un paquete RCON de un reader. Devuelve id, type, body.
func rconReadPacket(r *bufio.Reader) (id, typ int32, body string, err error) {
	var size int32
	if err = binary.Read(r, binary.LittleEndian, &size); err != nil {
		return 0, 0, "", err
	}
	if size < 10 || size > rconMaxBodySize+10 {
		return 0, 0, "", errRconBadPacket
	}
	payload := make([]byte, size)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, 0, "", err
	}
	id = int32(binary.LittleEndian.Uint32(payload[0:4]))
	typ = int32(binary.LittleEndian.Uint32(payload[4:8]))
	// Body va de 8 hasta el primer null (los 2 últimos bytes son nulls).
	bodyEnd := len(payload) - 2
	if bodyEnd < 8 {
		bodyEnd = 8
	}
	body = string(payload[8:bodyEnd])
	return id, typ, body, nil
}

// rconExecute abre una conexión, autentica, ejecuta UN comando y devuelve la
// respuesta. Cierra la conexión al terminar. Es la función de alto nivel que
// usa el endpoint. Maneja timeout en toda la operación.
func rconExecute(host string, port int, password, command string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// net.JoinHostPort pone corchetes a las IPv6 ("[::1]:25575"); un
	// fmt.Sprintf("%s:%d") las rompería (lo marcaba go vet).
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return "", fmt.Errorf("rcon: no se pudo conectar a %s: %w", addr, err)
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	_ = conn.SetDeadline(deadline)

	reader := bufio.NewReader(conn)

	// 1. AUTH · enviar password con un ID conocido.
	const authID = 1
	if _, err := conn.Write(rconBuildPacket(authID, rconTypeAuth, password)); err != nil {
		return "", fmt.Errorf("rcon: error enviando auth: %w", err)
	}
	// El server puede mandar un paquete vacío Type=0 antes del auth response;
	// leemos hasta encontrar el AUTH_RESPONSE (Type=2).
	for {
		rid, rtyp, _, err := rconReadPacket(reader)
		if err != nil {
			return "", fmt.Errorf("rcon: error leyendo auth: %w", err)
		}
		if rtyp == rconTypeAuthResponse {
			if rid == rconAuthFailID {
				return "", errRconAuthFailed
			}
			break // auth OK
		}
		// otros paquetes previos · ignorar y seguir leyendo
	}

	// 2. EXEC · enviar el comando.
	const execID = 2
	if _, err := conn.Write(rconBuildPacket(execID, rconTypeExecCommand, command)); err != nil {
		return "", fmt.Errorf("rcon: error enviando comando: %w", err)
	}

	// 3. RESPONSE · leer la respuesta (puede venir en varios paquetes para
	// respuestas largas; concatenamos los Type=0 que correspondan a execID).
	var out string
	for {
		rid, rtyp, body, err := rconReadPacket(reader)
		if err != nil {
			// Si ya tenemos algo, lo devolvemos (fin de respuesta por EOF/timeout).
			if out != "" {
				break
			}
			return "", fmt.Errorf("rcon: error leyendo respuesta: %w", err)
		}
		if rtyp == rconTypeResponse && rid == execID {
			out += body
			// Las respuestas largas se fragmentan; si el body es menor que el
			// máximo, asumimos que es el último paquete.
			if len(body) < rconMaxBodySize {
				break
			}
		}
	}

	return out, nil
}
