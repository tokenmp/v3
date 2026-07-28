#!/bin/bash

# TokenMP v3 E2E 测试运行脚本

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 打印帮助信息
show_help() {
    echo -e "${BLUE}TokenMP v3 E2E 测试运行脚本${NC}"
    echo ""
    echo "用法: $0 [选项]"
    echo ""
    echo "选项:"
    echo "  -h, --help          显示帮助信息"
    echo "  -a, --all           运行所有测试"
    echo "  -u, --ui            运行带 UI 的测试"
    echo "  -d, --debug         调试模式运行测试"
    echo "  -c, --codegen       生成测试代码"
    echo "  -r, --report        查看测试报告"
    echo "  -i, --install       安装依赖和浏览器"
    echo "  -s, --specific FILE 运行特定测试文件"
    echo "  -p, --project PROJ  运行特定项目（chromium, firefox, webkit, mobile-chrome, mobile-safari）"
    echo ""
    echo "示例:"
    echo "  $0 -a                # 运行所有测试"
    echo "  $0 -s tests/admin/users.spec.ts  # 运行用户管理测试"
    echo "  $0 -p chromium       # 只在 Chrome 中运行测试"
    echo "  $0 -d                # 调试模式运行测试"
}

# 检查是否在正确的目录
check_directory() {
    if [ ! -f "package.json" ]; then
        echo -e "${RED}错误: 请在 e2e 目录中运行此脚本${NC}"
        exit 1
    fi
}

# 安装依赖
install_dependencies() {
    echo -e "${BLUE}安装依赖...${NC}"
    pnpm install
    
    echo -e "${BLUE}安装浏览器...${NC}"
    pnpm install:browsers
    
    echo -e "${GREEN}安装完成！${NC}"
}

# 运行所有测试
run_all_tests() {
    echo -e "${BLUE}运行所有测试...${NC}"
    pnpm test
}

# 运行带 UI 的测试
run_ui_tests() {
    echo -e "${BLUE}运行带 UI 的测试...${NC}"
    pnpm test:ui
}

# 调试模式运行测试
run_debug_tests() {
    echo -e "${BLUE}调试模式运行测试...${NC}"
    pnpm test:debug
}

# 生成测试代码
run_codegen() {
    echo -e "${BLUE}生成测试代码...${NC}"
    pnpm test:codegen
}

# 查看测试报告
show_report() {
    echo -e "${BLUE}查看测试报告...${NC}"
    pnpm test:report
}

# 运行特定测试文件
run_specific_test() {
    local file=$1
    echo -e "${BLUE}运行特定测试: ${file}${NC}"
    pnpm test "$file"
}

# 运行特定项目
run_project_tests() {
    local project=$1
    echo -e "${BLUE}运行项目测试: ${project}${NC}"
    pnpm test --project="$project"
}

# 主函数
main() {
    check_directory
    
    case "${1:-}" in
        -h|--help)
            show_help
            ;;
        -a|--all)
            run_all_tests
            ;;
        -u|--ui)
            run_ui_tests
            ;;
        -d|--debug)
            run_debug_tests
            ;;
        -c|--codegen)
            run_codegen
            ;;
        -r|--report)
            show_report
            ;;
        -i|--install)
            install_dependencies
            ;;
        -s|--specific)
            if [ -z "${2:-}" ]; then
                echo -e "${RED}错误: 请指定测试文件${NC}"
                exit 1
            fi
            run_specific_test "$2"
            ;;
        -p|--project)
            if [ -z "${2:-}" ]; then
                echo -e "${RED}错误: 请指定项目名称${NC}"
                exit 1
            fi
            run_project_tests "$2"
            ;;
        "")
            show_help
            ;;
        *)
            echo -e "${RED}错误: 未知选项 $1${NC}"
            show_help
            exit 1
            ;;
    esac
}

# 运行主函数
main "$@"
