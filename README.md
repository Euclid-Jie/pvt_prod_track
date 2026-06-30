# pvt_prod_track

私募产品周报桌面应用。`pvt_prod_track.exe` 是原有桌面版，`pvt_prod_track_web.exe` 是独立的网页/手机服务版。

## 运行

桌面版：
```powershell
.\pvt_prod_track.exe
```

网页/手机服务版：
```powershell
.\pvt_prod_track_web.exe
```

默认监听 `0.0.0.0:5003`，页面地址是 `http://127.0.0.1:5003/`。

## 编译

先同步嵌入资源，再编译：
```powershell
Copy-Item templates\index.html go\assets\templates\index.html
Copy-Item templates\service.html go\assets\templates\service.html
Copy-Item static\style.css go\assets\static\style.css
```

桌面版：
```powershell
cd go
go build -ldflags="-H windowsgui" -o ..\pvt_prod_track.exe .
```

网页/手机服务版：
```powershell
cd go
go build -o ..\pvt_prod_track_web.exe .
```

调试版：
```powershell
cd go
go build -o ..\pvt_prod_track_debug.exe .
```

## 说明

- `pvt_prod_track.exe` 只保留原有桌面业务逻辑，继续走 WebView2 和本地随机端口。
- `pvt_prod_track_web.exe` 是单独的网页版，只用于浏览器和手机查看。
- 两个程序共用同一套 API 和数据层，但入口和页面是分开的。
