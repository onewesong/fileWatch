# Mac文件访问监控系统

这是一个使用Golang和Gin Web框架开发的Mac文件系统监控工具，用于记录和显示哪些程序正在访问文件系统。该工具利用macOS的`fs_usage`命令来获取文件访问信息，并将其保存到SQLite数据库中，通过Web界面进行展示。

## 功能特点

- 实时监控文件系统访问活动
- 记录进程名称、操作类型、文件路径等信息
- 提供按进程统计的文件访问次数
- 美观的Web界面展示监控数据
- 支持启动/停止监控功能

## 系统要求

- macOS操作系统
- Go 1.16或更高版本
- 管理员权限（运行`fs_usage`命令需要）

## 安装步骤

1. 克隆代码库

```bash
git clone https://github.com/yourusername/fileWatch.git
cd fileWatch
```

2. 安装依赖

```bash
go mod tidy
```

3. 构建项目

```bash
go build -o filewatch cmd/filewatch/main.go
```

## 使用方法

1. 运行应用程序

```bash
./filewatch
```

2. 在浏览器中访问 `http://localhost:8080`

3. 点击"开始监控"按钮开始收集文件访问数据

4. 使用Web界面查看和分析文件访问情况

## 注意事项

- 该程序需要管理员权限才能运行`fs_usage`命令
- 大量的文件系统活动可能会导致数据库迅速增长
- 为了减少记录数量，程序会过滤掉一些系统文件和临时文件的访问

## 项目结构

```
fileWatch/
├── cmd/
│   └── filewatch/        # 主程序入口
├── internal/
│   ├── api/              # Web API处理
│   ├── database/         # 数据库模型和操作
│   └── monitor/          # 文件监控模块
├── static/               # 静态资源文件
│   └── css/              # CSS样式文件
├── templates/            # HTML模板
├── go.mod                # Go模块文件
├── go.sum                # Go依赖校验文件
└── README.md             # 项目说明文档
```

## 技术栈

- Golang
- Gin Web框架
- SQLite数据库
- GORM ORM库
- JavaScript/HTML/CSS
- Bootstrap 5
- Chart.js 