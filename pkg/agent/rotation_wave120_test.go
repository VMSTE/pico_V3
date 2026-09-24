package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/pika"
)

// Волна 120 (срез 3): сигнал датчика приводит к реальной ротации
// (дефолтный агент тестового цикла уже на PikaSessionStore).
func TestRotateSessionWithNotice_Rotates(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	ts := newTurnState(agent, makeTestProcessOpts("test-session"), turnEventScope{
		turnID:  "turn-1",
		context: newTurnContext(nil, nil, nil),
	})

	db, err := pika.Migrate(":memory:")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer db.Close()
	bm, err := pika.NewBotMemory(db)
	if err != nil {
		t.Fatalf("botmemory: %v", err)
	}
	defer bm.Close()

	ps := pika.NewPikaSessionStore(bm)
	agent.Sessions = ps
	sl := ps.Session("test-session")
	sl.EnsureSession("test-session")
	oldID := sl.SessionID()
	if oldID == "" {
		t.Fatal("EnsureSession did not assign session ID")
	}

	// Ротируем явно подготовленный lifecycle (ts.sessionKey в тестовом
	// хелпере пустой; прод-путь берёт ключ из настоящего turnState).
	// ID = baseKey:unix-секунды — внутри одной секунды совпадает, поэтому
	// проверяем сам факт ротации через OnRotate-колбэк, а не смену ID.
	rotated := false
	sl.OnRotate(func(old string) {
		rotated = true
		if old != oldID {
			panic("callback got unexpected old session ID")
		}
	})
	al.rotateSessionWithNotice(context.Background(), ts, sl, "тест")

	if !rotated {
		t.Error("OnRotate callback not fired — rotation did not happen")
	}
}

// Волна 120-fix (бой 24 сен): после ротации счётчик цепочки обнуляется —
// 9-я абсолютная итерация (1-я после ротации) НЕ триггерит снова.
func TestCheckAndRotateSession_CounterResetsAfterRotation(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	db, err := pika.Migrate(":memory:")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer db.Close()
	bm, err := pika.NewBotMemory(db)
	if err != nil {
		t.Fatalf("botmemory: %v", err)
	}
	defer bm.Close()

	ps := pika.NewPikaSessionStore(bm)
	agent.Sessions = ps
	sl := ps.Session("test-session")
	sl.EnsureSession("test-session")

	ts := newTurnState(agent, makeTestProcessOpts("test-session"), turnEventScope{
		turnID:  "turn-1",
		context: newTurnContext(nil, nil, nil),
	})
	// Тестовый хелпер не заполняет ts.sessionKey (прод берёт из настоящего
	// turnState) — выставляем явно, чтобы checkAndRotateSession нашёл lifecycle.
	ts.sessionKey = "test-session"
	ctx := context.Background()

	rotations := 0
	sl.OnRotate(func(old string) { rotations++ })

	// 8 звеньев при низком контексте — ротация (триггер цепочки).
	al.checkAndRotateSession(ctx, ts, 10.0, 8)
	if rotations != 1 {
		t.Fatalf("rotations = %d, want 1 (chain trigger at 8)", rotations)
	}

	// 9-я абсолютная итерация = 1-я после ротации — НЕ триггерит.
	al.checkAndRotateSession(ctx, ts, 10.0, 9)
	if rotations != 1 {
		t.Fatalf("rotations = %d, want still 1 — counter must reset after rotation", rotations)
	}

	// 16-я абсолютная = 8-я после ротации — триггерит снова.
	al.checkAndRotateSession(ctx, ts, 10.0, 16)
	if rotations != 2 {
		t.Fatalf("rotations = %d, want 2 (next chain of 8)", rotations)
	}
}
