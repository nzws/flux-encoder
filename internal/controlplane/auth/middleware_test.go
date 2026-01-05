package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	// テストモードでログを無効化
	gin.SetMode(gin.TestMode)
}

func Test正しいAPIキーでリクエストが通過する(t *testing.T) {
	mustSetenv(t, "API_KEY", "test-api-key-123")
	defer func() {
		mustUnsetenv(t, "API_KEY")
	}()

	router := gin.New()
	router.Use(APIKeyMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer test-api-key-123")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ステータスコードが一致しない: 期待値 %d, 取得値 %d", http.StatusOK, w.Code)
	}
}

func Test間違ったAPIキーで401が返る(t *testing.T) {
	mustSetenv(t, "API_KEY", "test-api-key-123")
	defer func() {
		mustUnsetenv(t, "API_KEY")
	}()

	router := gin.New()
	router.Use(APIKeyMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-api-key")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("ステータスコードが一致しない: 期待値 %d, 取得値 %d", http.StatusUnauthorized, w.Code)
	}
}

func TestAuthorizationヘッダーがない場合は401が返る(t *testing.T) {
	mustSetenv(t, "API_KEY", "test-api-key-123")
	defer func() {
		mustUnsetenv(t, "API_KEY")
	}()

	router := gin.New()
	router.Use(APIKeyMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	// Authorization ヘッダーを設定しない
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("ステータスコードが一致しない: 期待値 %d, 取得値 %d", http.StatusUnauthorized, w.Code)
	}
}

func TestBearer形式でないAuthorizationヘッダーは401が返る(t *testing.T) {
	mustSetenv(t, "API_KEY", "test-api-key-123")
	defer func() {
		mustUnsetenv(t, "API_KEY")
	}()

	router := gin.New()
	router.Use(APIKeyMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	testCases := []struct {
		name   string
		header string
	}{
		{"Basic認証形式", "Basic dGVzdDp0ZXN0"},
		{"トークンのみ", "test-api-key-123"},
		{"空白のみ", " "},
		{"Bearerのみ", "Bearer"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("Authorization", tc.header)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("ステータスコードが一致しない: 期待値 %d, 取得値 %d", http.StatusUnauthorized, w.Code)
			}
		})
	}
}

func TestHealthエンドポイントは認証不要(t *testing.T) {
	mustSetenv(t, "API_KEY", "test-api-key-123")
	defer func() {
		mustUnsetenv(t, "API_KEY")
	}()

	router := gin.New()
	router.Use(APIKeyMiddleware())
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest("GET", "/health", nil)
	// Authorization ヘッダーを設定しない
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ステータスコードが一致しない: 期待値 %d, 取得値 %d", http.StatusOK, w.Code)
	}
}

func TestAPIキーが設定されていない場合は認証が無効化される(t *testing.T) {
	// API_KEY 環境変数を設定しない（またはクリア）
	mustUnsetenv(t, "API_KEY")

	router := gin.New()
	router.Use(APIKeyMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	// Authorization ヘッダーを設定しない
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// 認証が無効化されているので 200 が返るべき
	if w.Code != http.StatusOK {
		t.Errorf("ステータスコードが一致しない: 期待値 %d, 取得値 %d", http.StatusOK, w.Code)
	}
}

func TestAPIキーが設定されていない場合でもhealthエンドポイントは動作する(t *testing.T) {
	mustUnsetenv(t, "API_KEY")

	router := gin.New()
	router.Use(APIKeyMiddleware())
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ステータスコードが一致しない: 期待値 %d, 取得値 %d", http.StatusOK, w.Code)
	}
}

func Test複数のエンドポイントで認証が機能する(t *testing.T) {
	mustSetenv(t, "API_KEY", "test-api-key-123")
	defer func() {
		mustUnsetenv(t, "API_KEY")
	}()

	router := gin.New()
	router.Use(APIKeyMiddleware())
	router.GET("/api/v1/jobs", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"jobs": []string{}})
	})
	router.POST("/api/v1/jobs", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"id": "123"})
	})
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	testCases := []struct {
		name           string
		method         string
		path           string
		authHeader     string
		expectedStatus int
	}{
		{"GET /api/v1/jobs 認証あり", "GET", "/api/v1/jobs", "Bearer test-api-key-123", http.StatusOK},
		{"GET /api/v1/jobs 認証なし", "GET", "/api/v1/jobs", "", http.StatusUnauthorized},
		{"POST /api/v1/jobs 認証あり", "POST", "/api/v1/jobs", "Bearer test-api-key-123", http.StatusCreated},
		{"POST /api/v1/jobs 認証なし", "POST", "/api/v1/jobs", "", http.StatusUnauthorized},
		{"GET /health 認証なし", "GET", "/health", "", http.StatusOK},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("ステータスコードが一致しない: 期待値 %d, 取得値 %d", tc.expectedStatus, w.Code)
			}
		})
	}
}

func mustSetenv(t *testing.T, key, value string) {
	t.Helper()
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("環境変数の設定に失敗: %v", err)
	}
}

func mustUnsetenv(t *testing.T, key string) {
	t.Helper()
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("環境変数の削除に失敗: %v", err)
	}
}
